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
	"syscall"
)

func DirectoryScanner(directoryScanQueue chan string, fileReadQueue chan string, blockQueue chan Block, excludePatterns []string, workInProgress *sync.WaitGroup) {
	for directoryPath := range directoryScanQueue {
		if strings.HasPrefix(directoryPath, "/") {
			Logger.Fatalln("unable to create archive with absolute path reference:", directoryPath)
		}
		if Verbose {
			Logger.Println(directoryPath)
		}

		directory, err := os.Open(directoryPath)
		if err != nil {
			Logger.Println("directory read error:", err.Error())
			workInProgress.Done()
			continue
		}

		uid, gid, mode := getModeOwnership(directory)
		blockQueue <- Block{directoryPath, 0, nil, blockTypeDirectory, uid, gid, mode}

		for fileName := range readdirnames(directory) {
			filePath := filepath.Join(directoryPath, fileName)

			excludeFile := false
			for _, excludePattern := range excludePatterns {
				match, err := filepath.Match(excludePattern, filePath)
				if err == nil && match {
					excludeFile = true
					break
				}
			}
			if excludeFile {
				Logger.Println("skipping excluded file", filePath)
				continue
			}

			fileInfo, err := os.Lstat(filePath)
			if err != nil {
				Logger.Println("unable to lstat file", err.Error())
				continue
			} else if (fileInfo.Mode() & os.ModeSymlink) != 0 {
				Logger.Println("skipping symbolic link", filePath)
				continue
			}

			workInProgress.Add(1)
			if fileInfo.IsDir() {
				// Sending to directoryScanQueue can block if it's full; since
				// we're also the goroutine responsible for reading from it,
				// this could cause a deadlock.  We break that deadlock by
				// performing the send in a goroutine, where it can block
				// safely.  This does have the side-effect that
				// directoryScanQueue's max size is pretty much ineffective...
				// but that's better than a deadlock.
				go func(filePath string) {
					directoryScanQueue <- filePath
				}(filePath)
			} else {
				fileReadQueue <- filePath
			}
		}

		directory.Close()
		workInProgress.Done()
	}
}

func getModeOwnership(file *os.File) (int, int, os.FileMode) {
	var uid int = 0
	var gid int = 0
	var mode os.FileMode = 0
	fi, err := file.Stat()
	if err != nil {
		Logger.Println("file stat error; uid/gid/mode will be incorrect:", err.Error())
	} else {
		mode = fi.Mode()
		stat_t := fi.Sys().(*syscall.Stat_t)
		if stat_t != nil {
			uid = int(stat_t.Uid)
			gid = int(stat_t.Gid)
		} else {
			Logger.Println("unable to find file uid/gid")
		}
	}
	return uid, gid, mode
}

func FileReader(fileReadQueue <-chan string, blockQueue chan Block, workInProgress *sync.WaitGroup) {
	for filePath := range fileReadQueue {
		if Verbose {
			Logger.Println(filePath)
		}

		file, err := os.Open(filePath)
		if err == nil {

			uid, gid, mode := getModeOwnership(file)
			blockQueue <- Block{filePath, 0, nil, blockTypeStartOfFile, uid, gid, mode}

			bufferedFile := bufio.NewReader(file)

			for {
				buffer := make([]byte, BlockSize)
				bytesRead, err := bufferedFile.Read(buffer)
				if err == io.EOF {
					break
				} else if err != nil {
					Logger.Println("file read error; file contents will be incomplete:", err.Error())
					break
				}

				blockQueue <- Block{filePath, uint16(bytesRead), buffer, blockTypeData, 0, 0, 0}
			}

			blockQueue <- Block{filePath, 0, nil, blockTypeEndOfFile, 0, 0, 0}
			file.Close()
		} else {
			Logger.Println("file open error:", err.Error())
		}

		workInProgress.Done()
	}
}

func (b *Block) writeBlock(output io.Writer) error {
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
			Logger.Panicln("Unexpected block type")
		}
	}
	return err
}

func ArchiveWriter(output io.Writer, blockQueue <-chan Block) {
	hash := crc64.New(crc64.MakeTable(crc64.ECMA))
	output = io.MultiWriter(output, hash)
	blockCount := 0

	_, err := output.Write(fastArchiverHeader)
	if err != nil {
		Logger.Fatalln("Archive write error:", err.Error())
	}

	for block := range blockQueue {
		err = block.writeBlock(output)

		blockCount += 1
		if err == nil && (blockCount%1000) == 0 {
			err = writeChecksumBlock(hash, output)
		}

		if err != nil {
			Logger.Fatalln("Archive write error:", err.Error())
		}
	}

	err = writeChecksumBlock(hash, output)
	if err != nil {
		Logger.Fatalln("Archive write error:", err.Error())
	}
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
func readdirnames(dir *os.File) chan string {
	retval := make(chan string, 256)
	go func(dir *os.File) {
		for {
			names, err := dir.Readdirnames(256)
			if err == io.EOF {
				break
			} else if err != nil {
				Logger.Println("error reading directory:", err.Error())
			}
			for _, name := range names {
				retval <- name
			}
		}
		close(retval)
	}(dir)
	return retval
}
