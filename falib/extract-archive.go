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

func ArchiveReader(file io.Reader) {
	var workInProgress sync.WaitGroup
	fileOutputChan := make(map[string]chan Block)

	hashReader := hashingReader{file, crc64.New(crc64.MakeTable(crc64.ECMA))}
	file = hashReader

	fileHeader := make([]byte, 8)
	_, err := io.ReadFull(file, fileHeader)
	if err != nil {
		Logger.Fatalln("Archive read error:", err.Error())
	} else if !bytes.Equal(fileHeader, fastArchiverHeader) {
		Logger.Fatalln("Archive header not recognized")
	}

	for {
		var pathSize uint16
		err = binary.Read(file, binary.BigEndian, &pathSize)
		if err == io.EOF {
			break
		} else if err != nil {
			Logger.Fatalln("Archive read error:", err.Error())
		}

		buf := make([]byte, pathSize)
		_, err = io.ReadFull(file, buf)
		if err != nil {
			Logger.Fatalln("Archive read error:", err.Error())
		}
		filePath := string(buf)
		if strings.HasPrefix(filePath, "/") {
			Logger.Fatalln("unable to extract archive with absolute path reference:", filePath)
		}

		blockType := make([]byte, 1)
		_, err = io.ReadFull(file, blockType)
		if err != nil {
			Logger.Fatalln("Archive read error:", err.Error())
		}

		if blockType[0] == byte(blockTypeStartOfFile) {
			var uid uint32
			var gid uint32
			var mode os.FileMode

			err = binary.Read(file, binary.BigEndian, &uid)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}

			err = binary.Read(file, binary.BigEndian, &gid)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}

			err = binary.Read(file, binary.BigEndian, &mode)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}

			c := make(chan Block, 1)
			fileOutputChan[filePath] = c
			workInProgress.Add(1)
			go writeFile(c, &workInProgress)
			c <- Block{filePath, 0, nil, blockTypeStartOfFile, int(uid), int(gid), mode}
		} else if blockType[0] == byte(blockTypeEndOfFile) {
			c := fileOutputChan[filePath]
			c <- Block{filePath, 0, nil, blockTypeEndOfFile, 0, 0, 0}
			close(c)
			delete(fileOutputChan, filePath)
		} else if blockType[0] == byte(blockTypeData) {
			var blockSize uint16
			err = binary.Read(file, binary.BigEndian, &blockSize)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}

			blockData := make([]byte, blockSize)
			_, err = io.ReadFull(file, blockData)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}

			c := fileOutputChan[filePath]
			c <- Block{filePath, blockSize, blockData, blockTypeData, 0, 0, 0}
		} else if blockType[0] == byte(blockTypeDirectory) {
			var uid uint32
			var gid uint32
			var mode os.FileMode

			err = binary.Read(file, binary.BigEndian, &uid)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}
			err = binary.Read(file, binary.BigEndian, &gid)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}
			err = binary.Read(file, binary.BigEndian, &mode)
			if err != nil {
				Logger.Fatalln("Archive read error:", err.Error())
			}

			if IgnorePerms {
				mode = os.ModeDir | 0755
			}
			err = os.Mkdir(filePath, mode)
			if err != nil && !os.IsExist(err) {
				Logger.Fatalln("Directory create error:", err.Error())
			}
			if !IgnoreOwners {
				err = os.Chown(filePath, int(uid), int(gid))
				if err != nil {
					Logger.Println("Directory chown error:", err.Error())
				}
			}
		} else if blockType[0] == byte(blockTypeChecksum) {
			currentChecksum := hashReader.hasher.Sum64()

			var expectedChecksum uint64
			binary.Read(file, binary.BigEndian, &expectedChecksum)

			if expectedChecksum != currentChecksum {
				Logger.Fatalln("crc64 mismatch, expected", expectedChecksum, "was", currentChecksum)
			}
		} else {
			Logger.Fatalln("Archive error: unrecognized block type", blockType[0])
		}
	}

	workInProgress.Wait()
}

func writeFile(blockSource chan Block, workInProgress *sync.WaitGroup) {
	var file *os.File = nil
	var bufferedFile *bufio.Writer
	for block := range blockSource {
		if block.blockType == blockTypeStartOfFile {
			if Verbose {
				Logger.Println(block.filePath)
			}

			tmp, err := os.Create(block.filePath)
			if err != nil {
				Logger.Fatalln("File create error:", err.Error())
			}
			file = tmp
			bufferedFile = bufio.NewWriter(file)

			if !IgnoreOwners {
				err = file.Chown(block.uid, block.gid)
				if err != nil {
					Logger.Println("Unable to chown file to", block.uid, "/", block.gid, ":", err.Error())
				}
			}
			if !IgnorePerms {
				err = file.Chmod(block.mode)
				if err != nil {
					Logger.Println("Unable to chmod file to", block.mode, ":", err.Error())
				}
			}
		} else if block.blockType == blockTypeEndOfFile {
			bufferedFile.Flush()
			file.Close()
			file = nil
		} else {
			_, err := bufferedFile.Write(block.buffer[:block.numBytes])
			if err != nil {
				Logger.Fatalln("File write error:", err.Error())
			}
		}
	}
	workInProgress.Done()
}
