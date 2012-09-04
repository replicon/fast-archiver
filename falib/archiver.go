package falib

import (
	"bufio"
	"encoding/binary"
	"hash"
	"hash/crc64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Archiver struct {
	DirReaderCount    int
	FileReaderCount   int
	DirScanQueueSize  int
	FileReadQueueSize int
	BlockQueueSize    int
	ExcludePatterns   []string
	Logger            Logger
	BlockSize         uint16

	directoryScanQueue chan string
	fileReadQueue      chan string
	blockQueue         chan block
	workInProgress     sync.WaitGroup
	excludePatterns    []string
	output             *bufio.Writer
	error              error
}

func NewArchiver(output io.Writer) *Archiver {
	retval := &Archiver{}
	retval.ExcludePatterns = []string{}
	retval.output = bufio.NewWriter(output)
	retval.DirReaderCount = 16
	retval.FileReaderCount = 16
	retval.DirScanQueueSize = 128
	retval.FileReadQueueSize = 128
	retval.BlockQueueSize = 128
	retval.BlockSize = 4096
	return retval
}

func (a *Archiver) AddDir(directoryPath string) {
	if a.directoryScanQueue == nil {
		a.directoryScanQueue = make(chan string, a.DirScanQueueSize)
	}
	a.workInProgress.Add(1)
	a.directoryScanQueue <- directoryPath
}

func (a *Archiver) Run() error {
	if a.directoryScanQueue == nil {
		a.directoryScanQueue = make(chan string, a.DirScanQueueSize)
	}
	a.fileReadQueue = make(chan string, a.FileReadQueueSize)
	a.blockQueue = make(chan block, a.BlockQueueSize)
	a.error = nil

	for i := 0; i < a.DirReaderCount; i++ {
		go a.directoryScanner()
	}
	for i := 0; i < a.FileReaderCount; i++ {
		go a.fileReader()
	}

	go func() {
		a.workInProgress.Wait()
		close(a.directoryScanQueue)
		close(a.fileReadQueue)
		close(a.blockQueue)
	}()

	err := a.archiveWriter()
	a.output.Flush()

	if err != nil {
		return err
	}
	return a.error
}

func (a *Archiver) directoryScanner() {
	for directoryPath := range a.directoryScanQueue {
		if strings.HasPrefix(directoryPath, "/") {
			a.error = ErrAbsoluteDirectoryPath
			a.workInProgress.Done()
			continue
		}
		a.Logger.Verbose(directoryPath)

		directory, err := os.Open(directoryPath)
		if err != nil {
			a.Logger.Warning("directory read error:", err.Error())
			a.workInProgress.Done()
			continue
		}

		uid, gid, mode := a.getModeOwnership(directory)
		a.blockQueue <- block{directoryPath, 0, nil, blockTypeDirectory, uid, gid, mode}

		for fileName := range a.readdirnames(directory) {
			filePath := filepath.Join(directoryPath, fileName)

			excludeFile := false
			for _, excludePattern := range a.excludePatterns {
				match, err := filepath.Match(excludePattern, filePath)
				if err == nil && match {
					excludeFile = true
					break
				}
			}
			if excludeFile {
				a.Logger.Verbose("skipping excluded file", filePath)
				continue
			}

			fileInfo, err := os.Lstat(filePath)
			if err != nil {
				a.Logger.Warning("unable to lstat file", err.Error())
				continue
			} else if (fileInfo.Mode() & os.ModeSymlink) != 0 {
				a.Logger.Warning("skipping symbolic link", filePath)
				continue
			}

			a.workInProgress.Add(1)
			if fileInfo.IsDir() {
				// Sending to directoryScanQueue can block if it's full; since
				// we're also the goroutine responsible for reading from it,
				// this could cause a deadlock.  We break that deadlock by
				// performing the send in a goroutine, where it can block
				// safely.  This does have the side-effect that
				// directoryScanQueue's max size is pretty much ineffective...
				// but that's better than a deadlock.
				go func(filePath string) {
					a.directoryScanQueue <- filePath
				}(filePath)
			} else {
				a.fileReadQueue <- filePath
			}
		}

		directory.Close()
		a.workInProgress.Done()
	}
}

func (a *Archiver) fileReader() {
	for filePath := range a.fileReadQueue {
		a.Logger.Verbose(filePath)

		file, err := os.Open(filePath)
		if err == nil {

			uid, gid, mode := a.getModeOwnership(file)
			a.blockQueue <- block{filePath, 0, nil, blockTypeStartOfFile, uid, gid, mode}

			bufferedFile := bufio.NewReader(file)

			for {
				buffer := make([]byte, a.BlockSize)
				bytesRead, err := bufferedFile.Read(buffer)
				if err == io.EOF {
					break
				} else if err != nil {
					a.Logger.Warning("file read error; file contents will be incomplete:", err.Error())
					break
				}

				a.blockQueue <- block{filePath, uint16(bytesRead), buffer, blockTypeData, 0, 0, 0}
			}

			a.blockQueue <- block{filePath, 0, nil, blockTypeEndOfFile, 0, 0, 0}
			file.Close()
		} else {
			a.Logger.Warning("file open error:", err.Error())
		}

		a.workInProgress.Done()
	}
}

func (b *block) writeBlock(output io.Writer) error {
	filePath := []byte(b.filePath)
	err := binary.Write(output, binary.BigEndian, uint16(len(filePath)))
	if err == nil {
		_, err = output.Write(filePath)
	}
	if err == nil {
		blockType := []byte{byte(b.blockType)}
		_, err = output.Write(blockType)
	}
	if err == nil {
		switch b.blockType {
		case blockTypeDirectory, blockTypeStartOfFile:
			err = binary.Write(output, binary.BigEndian, uint32(b.uid))
			if err == nil {
				err = binary.Write(output, binary.BigEndian, uint32(b.gid))
			}
			if err == nil {
				err = binary.Write(output, binary.BigEndian, b.mode)
			}
		case blockTypeEndOfFile:
			// Nothing to write aside from the block type
		case blockTypeData:
			err = binary.Write(output, binary.BigEndian, uint16(b.numBytes))
			if err == nil {
				_, err = output.Write(b.buffer[:b.numBytes])
			}
		default:
			panic("Internal error: unexpected block type")
		}
	}
	return err
}

func (a *Archiver) archiveWriter() error {
	hash := crc64.New(crc64.MakeTable(crc64.ECMA))
	output := io.MultiWriter(a.output, hash)
	blockCount := 0

	_, err := output.Write(fastArchiverHeader)
	if err != nil {
		return err
	}

	for block := range a.blockQueue {
		err = block.writeBlock(output)

		blockCount += 1
		if err == nil && (blockCount%1000) == 0 {
			err = writeChecksumBlock(hash, output)
		}

		if err != nil {
			return err
		}
	}

	return writeChecksumBlock(hash, output)
}

func writeChecksumBlock(hash hash.Hash64, output io.Writer) error {
	// file path length... zero
	err := binary.Write(output, binary.BigEndian, uint16(0))
	if err == nil {
		blockType := []byte{byte(blockTypeChecksum)}
		_, err = output.Write(blockType)
	}
	if err == nil {
		err = binary.Write(output, binary.BigEndian, hash.Sum64())
	}
	return err
}

// Wrapper for Readdirnames that converts it into a generator-style method.
func (a *Archiver) readdirnames(dir *os.File) chan string {
	retval := make(chan string, 256)
	go func(dir *os.File) {
		for {
			names, err := dir.Readdirnames(256)
			if err == io.EOF {
				break
			} else if err != nil {
				a.Logger.Warning("error reading directory:", err.Error())
			}
			for _, name := range names {
				retval <- name
			}
		}
		close(retval)
	}(dir)
	return retval
}
