package falib

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"hash"
	"hash/crc64"
	"io"
	"os"
	"strings"
	"sync"
)

// An io.Reader implementation that also keeps a crc64 as it reads.  Fancy!
type hashingReader struct {
	innerReader io.Reader
	hasher      hash.Hash64
}

func (r hashingReader) Read(buf []byte) (int, error) {
	n, err := r.innerReader.Read(buf)
	if err == nil {
		r.hasher.Write(buf[:n])
	}
	return n, err
}

type Unarchiver struct {
	Logger       Logger
	IgnorePerms  bool
	IgnoreOwners bool
	DryRun       bool

	file io.Reader
}

func NewUnarchiver(file io.Reader) *Unarchiver {
	retval := &Unarchiver{}
	retval.file = bufio.NewReader(file)
	return retval
}

func (u *Unarchiver) Run() error {
	var workInProgress sync.WaitGroup
	fileOutputChan := make(map[string]chan block)

	reader := hashingReader{u.file, crc64.New(crc64.MakeTable(crc64.ECMA))}

	fileHeader := make([]byte, 8)
	_, err := io.ReadFull(reader, fileHeader)
	if err != nil {
		return err
	} else if !bytes.Equal(fileHeader, fastArchiverHeader) {
		return ErrFileHeaderMismatch
	}

	for {
		var pathSize uint16
		err = binary.Read(reader, binary.BigEndian, &pathSize)
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		buf := make([]byte, pathSize)
		_, err = io.ReadFull(reader, buf)
		if err != nil {
			return err
		}
		filePath := string(buf)
		if strings.HasPrefix(filePath, "/") {
			return ErrAbsoluteDirectoryPath
		}

		blockType := make([]byte, 1)
		_, err = io.ReadFull(reader, blockType)
		if err != nil {
			return err
		}

		if blockType[0] == byte(blockTypeStartOfFile) {
			var uid uint32
			var gid uint32
			var mode os.FileMode

			err = binary.Read(reader, binary.BigEndian, &uid)
			if err != nil {
				return err
			}

			err = binary.Read(reader, binary.BigEndian, &gid)
			if err != nil {
				return err
			}

			err = binary.Read(reader, binary.BigEndian, &mode)
			if err != nil {
				return err
			}

			c := make(chan block, 1)
			fileOutputChan[filePath] = c
			workInProgress.Add(1)
			go u.writeFile(c, &workInProgress)
			c <- block{filePath, 0, nil, blockTypeStartOfFile, int(uid), int(gid), mode}
		} else if blockType[0] == byte(blockTypeEndOfFile) {
			c := fileOutputChan[filePath]
			c <- block{filePath, 0, nil, blockTypeEndOfFile, 0, 0, 0}
			close(c)
			delete(fileOutputChan, filePath)
		} else if blockType[0] == byte(blockTypeData) {
			var blockSize uint16
			err = binary.Read(reader, binary.BigEndian, &blockSize)
			if err != nil {
				return err
			}

			blockData := make([]byte, blockSize)
			_, err = io.ReadFull(reader, blockData)
			if err != nil {
				return err
			}

			c := fileOutputChan[filePath]
			c <- block{filePath, blockSize, blockData, blockTypeData, 0, 0, 0}
		} else if blockType[0] == byte(blockTypeDirectory) {
			var uid uint32
			var gid uint32
			var mode os.FileMode

			err = binary.Read(reader, binary.BigEndian, &uid)
			if err != nil {
				return err
			}
			err = binary.Read(reader, binary.BigEndian, &gid)
			if err != nil {
				return err
			}
			err = binary.Read(reader, binary.BigEndian, &mode)
			if err != nil {
				return err
			}

			if u.IgnorePerms {
				mode = os.ModeDir | 0755
			}

			if u.DryRun {
				continue
			}

			err = os.Mkdir(filePath, mode)
			if err != nil && !os.IsExist(err) {
				return err
			}
			if !u.IgnoreOwners {
				err = os.Chown(filePath, int(uid), int(gid))
				if err != nil {
					u.Logger.Warning("Directory chown error:", err.Error())
				}
			}
		} else if blockType[0] == byte(blockTypeChecksum) {
			currentChecksum := reader.hasher.Sum64()

			var expectedChecksum uint64
			binary.Read(reader, binary.BigEndian, &expectedChecksum)

			if expectedChecksum != currentChecksum {
				return ErrCrcMismatch
			}
		} else {
			return ErrUnrecognizedBlockType
		}
	}

	workInProgress.Wait()

	return nil
}

func (u *Unarchiver) writeFile(blockSource chan block, workInProgress *sync.WaitGroup) {
	var file *os.File = nil
	var bufferedFile *bufio.Writer
	for block := range blockSource {
		if block.blockType == blockTypeStartOfFile {
			u.Logger.Verbose(block.filePath)

			if u.DryRun {
				continue
			}

			tmp, err := os.Create(block.filePath)
			if err != nil {
				u.Logger.Warning("File create error:", err.Error())
				file = nil
				continue
			}
			file = tmp
			bufferedFile = bufio.NewWriter(file)

			if !u.IgnoreOwners {
				err = file.Chown(block.uid, block.gid)
				if err != nil {
					u.Logger.Warning("Unable to chown file to", block.uid, "/", block.gid, ":", err.Error())
				}
			}
			if !u.IgnorePerms {
				err = file.Chmod(block.mode)
				if err != nil {
					u.Logger.Warning("Unable to chmod file to", block.mode, ":", err.Error())
				}
			}
		} else if file == nil {
			// do nothing; file couldn't be opened for write
		} else if block.blockType == blockTypeEndOfFile {
			bufferedFile.Flush()
			file.Close()
			file = nil
		} else {
			_, err := bufferedFile.Write(block.buffer[:block.numBytes])
			if err != nil {
				u.Logger.Warning("File write error:", err.Error())
			}
		}
	}
	workInProgress.Done()
}
