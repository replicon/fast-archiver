package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

func directoryScanner(directoryScanQueue chan string, fileReadQueue chan string, fileWriterQueue chan block, excludePatterns []string, workInProgress *sync.WaitGroup) {
	for directoryPath := range directoryScanQueue {
		if verbose {
			logger.Println(directoryPath)
		}

		directory, err := os.Open(directoryPath)
		if err != nil {
			logger.Println("directory read error:", err.Error())
			workInProgress.Done()
			continue
		}

		uid, gid, mode := getModeOwnership(directory)
		workInProgress.Add(1)
		fileWriterQueue <- block{directoryPath, 0, nil, blockTypeDirectory, uid, gid, mode}

		for fileName := range readdirnames(int(directory.Fd())) {
			workInProgress.Add(1)
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
				logger.Println("skipping excluded file", filePath)
				workInProgress.Done()
				continue
			}

			fileInfo, err := os.Lstat(filePath)
			if err != nil {
				logger.Println("unable to lstat file", err.Error())
				workInProgress.Done()
				continue
			} else if (fileInfo.Mode() & os.ModeSymlink) != 0 {
				logger.Println("skipping symbolic link", filePath)
				workInProgress.Done()
				continue
			}

			if fileInfo.IsDir() {
				directoryScanQueue <- filePath
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
		logger.Println("file stat error; uid/gid/mode will be incorrect:", err.Error())
	} else {
		mode = fi.Mode()
		stat_t := fi.Sys().(*syscall.Stat_t)
		if stat_t != nil {
			uid = int(stat_t.Uid)
			gid = int(stat_t.Gid)
		} else {
			logger.Println("unable to find file uid/gid")
		}
	}
	return uid, gid, mode
}

func fileReader(fileReadQueue <-chan string, fileWriterQueue chan block, workInProgress *sync.WaitGroup) {
	for filePath := range fileReadQueue {
		if verbose {
			logger.Println(filePath)
		}

		file, err := os.Open(filePath)
		if err == nil {

			uid, gid, mode := getModeOwnership(file)

			workInProgress.Add(1)
			fileWriterQueue <- block{filePath, 0, nil, blockTypeStartOfFile, uid, gid, mode}

			bufferedFile := bufio.NewReader(file)

			for {
				buffer := make([]byte, blockSize)
				bytesRead, err := bufferedFile.Read(buffer)
				if err == io.EOF {
					break
				} else if err != nil {
					logger.Println("file read error; file contents will be incomplete:", err.Error())
					break
				}

				workInProgress.Add(1)
				fileWriterQueue <- block{filePath, uint16(bytesRead), buffer, blockTypeData, 0, 0, 0}
			}

			workInProgress.Add(1)
			fileWriterQueue <- block{filePath, 0, nil, blockTypeEndOfFile, 0, 0, 0}

			file.Close()
		} else {
			logger.Println("file open error:", err.Error())
		}

		workInProgress.Done()
	}
}

func archiveWriter(output io.Writer, fileWriterQueue <-chan block, workInProgress *sync.WaitGroup) {
	flags := make([]byte, 1)

	for block := range fileWriterQueue {
		filePath := []byte(block.filePath)
		err := binary.Write(output, binary.BigEndian, uint16(len(filePath)))
		if err != nil {
			logger.Fatalln("Archive write error:", err.Error())
		}
		_, err = output.Write(filePath)
		if err != nil {
			logger.Fatalln("Archive write error:", err.Error())
		}

		if block.blockType == blockTypeStartOfFile {
			flags[0] = startOfFileFlag
			_, err = output.Write(flags)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
			err = binary.Write(output, binary.BigEndian, uint32(block.uid))
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
			err = binary.Write(output, binary.BigEndian, uint32(block.gid))
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
			err = binary.Write(output, binary.BigEndian, block.mode)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
		} else if block.blockType == blockTypeEndOfFile {
			flags[0] = endOfFileFlag
			_, err = output.Write(flags)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
		} else if block.blockType == blockTypeData {
			flags[0] = dataBlockFlag
			_, err = output.Write(flags)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}

			err = binary.Write(output, binary.BigEndian, uint16(block.numBytes))
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}

			_, err = output.Write(block.buffer[:block.numBytes])
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
		} else if block.blockType == blockTypeDirectory {
			flags[0] = directoryFlag
			_, err = output.Write(flags)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
			err = binary.Write(output, binary.BigEndian, uint32(block.uid))
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
			err = binary.Write(output, binary.BigEndian, uint32(block.gid))
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
			err = binary.Write(output, binary.BigEndian, block.mode)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
		} else {
			logger.Fatalln("Unexpected block type")
		}

		workInProgress.Done()
	}
}

// Copy of os.Readdirnames for UNIX systems, but modified to return results
// as found through a channel rather than in one large array.
func readdirnames(fd int) chan string {
	retval := make(chan string)
	go func(fd int) {
		var buf []byte = make([]byte, blockSize)
		var nbuf int
		var bufp int

		for {
			// Refill the buffer if necessary
			if bufp >= nbuf {
				bufp = 0
				var errno error
				nbuf, errno = syscall.ReadDirent(fd, buf)
				if errno != nil {
					err := os.NewSyscallError("readdirent", errno)
					logger.Println("error reading directory:", err.Error())
					break
				}
				if nbuf <= 0 {
					break // EOF
				}
			}

			// Drain the buffer
			var nb, nc int
			names := make([]string, 0, 100)
			nb, nc, names = syscall.ParseDirent(buf[bufp:nbuf], -1, names)
			bufp += nb

			for i := 0; i < nc; i++ {
				retval <- names[i]
			}
		}

		close(retval)
	}(fd)
	return retval
}
