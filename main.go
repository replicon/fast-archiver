package main

import (
	"encoding/binary"
	"flag"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

type block struct {
	filePath    string
	numBytes    uint16
	buffer      []byte
	startOfFile bool
	endOfFile   bool
}

var blockSize = 4096
const dataBlockFlag byte = 1 << 0
const startOfFileFlag byte = 1 << 1
const endOfFileFlag byte = 1 << 2

func main() {
	extract := flag.Bool("x", false, "extract archive")
	create := flag.Bool("c", false, "create archive")
	flag.Parse()

	if *extract {
		archiveReader("test.output")

	} else if *create {
		var directoryScanQueue = make(chan string, 128)
		var fileReadQueue = make(chan string, 128)
		var fileWriteQueue = make(chan block, 128)
		var workInProgress sync.WaitGroup

		go archiveWriter(fileWriteQueue, &workInProgress)

		for i := 0; i < 16; i++ {
			go directoryScanner(directoryScanQueue, fileReadQueue, &workInProgress)
		}
		for i := 0; i < 16; i++ {
			go fileReader(fileReadQueue, fileWriteQueue, &workInProgress)
		}

		workInProgress.Add(1)
		directoryScanQueue <- "test"

		workInProgress.Wait()
		close(directoryScanQueue)
		close(fileReadQueue)
		close(fileWriteQueue)
	} else {
		println("extract (-x) or create (-c) flag must be provided")
		os.Exit(4)
	}
}

func directoryScanner(directoryScanQueue chan string, fileReadQueue chan string, workInProgress *sync.WaitGroup) {
	for directoryPath := range directoryScanQueue {
		files, err := ioutil.ReadDir(directoryPath)
		if err != nil {
			println("Directory read error:", err.Error())
			os.Exit(1)
		}

		workInProgress.Add(len(files))
		for _, file := range files {
			filePath := filepath.Join(directoryPath, file.Name())
			if file.IsDir() {
				directoryScanQueue <- filePath
			} else {
				fileReadQueue <- filePath
			}
		}

		workInProgress.Done()
	}
}

func fileReader(fileReadQueue <-chan string, fileWriterQueue chan block, workInProgress *sync.WaitGroup) {
	for filePath := range fileReadQueue {
		file, err := os.Open(filePath)
		if err != nil {
			println("File open error:", err.Error())
			os.Exit(2)
		}

		workInProgress.Add(1)
		fileWriterQueue <- block{filePath, 0, nil, true, false}

		for {
			buffer := make([]byte, blockSize)
			bytesRead, err := file.Read(buffer)
			if err == io.EOF {
				break
			} else if err != nil {
				println("File read error:", err.Error())
				os.Exit(2)
			}

			workInProgress.Add(1)
			fileWriterQueue <- block{filePath, uint16(bytesRead), buffer, false, false}
		}

		workInProgress.Add(1)
		fileWriterQueue <- block{filePath, 0, nil, false, true}

		file.Close()
		workInProgress.Done()
	}
}

func archiveWriter(fileWriterQueue <-chan block, workInProgress *sync.WaitGroup) {
	output, err := os.Create("test.output")
	if err != nil {
		println("File output write error:", err.Error())
		os.Exit(3)
	}

	flags := make([]byte, 1)

	for block := range fileWriterQueue {
		filePath := []byte(block.filePath)
		err = binary.Write(output, binary.BigEndian, uint16(len(filePath)))
		if err != nil {
			println("File output write error:", err.Error())
			os.Exit(3)
		}
		_, err = output.Write(filePath)
		if err != nil {
			println("File output write error:", err.Error())
			os.Exit(3)
		}

		if block.startOfFile {
			flags[0] = startOfFileFlag
			_, err = output.Write(flags)
			if err != nil {
				println("File output write error:", err.Error())
				os.Exit(3)
			}
		} else if block.endOfFile {
			flags[0] = endOfFileFlag
			_, err = output.Write(flags)
			if err != nil {
				println("File output write error:", err.Error())
				os.Exit(3)
			}
		} else {
			flags[0] = dataBlockFlag
			_, err = output.Write(flags)
			if err != nil {
				println("File output write error:", err.Error())
				os.Exit(3)
			}

			err = binary.Write(output, binary.BigEndian, uint16(block.numBytes))
			if err != nil {
				println("File output write error:", err.Error())
				os.Exit(3)
			}

			_, err = output.Write(block.buffer[:block.numBytes])
			if err != nil {
				println("File output write error:", err.Error())
				os.Exit(3)
			}
		}

		workInProgress.Done()
	}
}

func archiveReader(archiveFileName string) {
	var workInProgress sync.WaitGroup

	file, err := os.Open(archiveFileName)
	if err != nil {
		println("File open error:", err.Error())
		os.Exit(2)
	}

	fileOutputChan := make(map[string]chan block)

	for {
		var pathSize uint16
		err = binary.Read(file, binary.BigEndian, &pathSize)
		if err == io.EOF {
			break
		} else if err != nil {
			println("File read error:", err.Error())
			os.Exit(2)
		}

		buf := make([]byte, pathSize)
		_, err = io.ReadFull(file, buf)
		if err != nil {
			println("File read error:", err.Error())
			os.Exit(2)
		}
		filePath := string(buf)

		flag := make([]byte, 1)
		_, err = io.ReadFull(file, flag)
		if err != nil {
			println("File read error:", err.Error())
			os.Exit(2)
		}

		if flag[0] == startOfFileFlag {

			c := make(chan block, 1)
			fileOutputChan[filePath] = c
			workInProgress.Add(1)
			go writeFile(c, &workInProgress)
			c <- block{ filePath, 0, nil, true, false }

		} else if flag[0] == endOfFileFlag {

			c := fileOutputChan[filePath]
			c <- block{ filePath, 0, nil, false, true }
			close(c)
			delete(fileOutputChan, filePath)

		} else if flag[0] == dataBlockFlag {

			var blockSize uint16
			err = binary.Read(file, binary.BigEndian, &blockSize)
			if err != nil {
				println("File read error:", err.Error())
				os.Exit(2)
			}

			blockData := make([]byte, blockSize)
			_, err = io.ReadFull(file, blockData)
			if err != nil {
				println("File read error:", err.Error())
				os.Exit(2)
			}

			c := fileOutputChan[filePath]
			c <- block{ filePath, blockSize, blockData, false, false }

		} else {
			println("unrecognized block flag")
			os.Exit(2)
		}
	}

	file.Close()
	workInProgress.Wait()
}

func writeFile(blockSource chan block, workInProgress *sync.WaitGroup) {
	var file *os.File = nil
	for block := range blockSource {
		if block.startOfFile {

			dir, _ := filepath.Split(block.filePath)
			err := os.MkdirAll(dir, os.ModeDir | 0755)
			if err != nil {
				println("Directory create error:", err.Error())
				os.Exit(4)
			}

			tmp, err := os.Create(block.filePath)
			if err != nil {
				println("File create error:", err.Error())
				os.Exit(4)
			}
			file = tmp
		} else if block.endOfFile {
			file.Close()
			file = nil
		} else {
			_, err := file.Write(block.buffer[:block.numBytes])
			if err != nil {
				println("File write error:", err.Error())
				os.Exit(4)
			}
		}
	}
	workInProgress.Done()
}

