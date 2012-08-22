package main

import (
	"encoding/binary"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

type block struct {
	filePath      string
	numBytes      uint16
	buffer        []byte
	writeComplete chan bool
}

var directoriesPending = 0
var filesPending = 0

func main() {
	var directoryScanQueue = make(chan string, 128)
	var fileReadQueue = make(chan string, 128)
	var fileWriteQueue = make(chan block, 1)
	var workInProgress sync.WaitGroup

	go fileWriter(fileWriteQueue)

	for i := 0; i < 1; i++ {
		go directoryScanner(directoryScanQueue, fileReadQueue, &workInProgress)
	}
	for i := 0; i < 1; i++ {
		go fileReader(fileReadQueue, fileWriteQueue, &workInProgress)
	}

	workInProgress.Add(1)
	directoryScanQueue <- "test"

	workInProgress.Wait()
	close(directoryScanQueue)
	close(fileReadQueue)
	close(fileWriteQueue)
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
	buffer := make([]byte, 4096)
	writeComplete := make(chan bool)

	for filePath := range fileReadQueue {
		file, err := os.Open(filePath)
		if err != nil {
			println("File open error:", err.Error())
			os.Exit(2)
		}

		for {
			bytesRead, err := file.Read(buffer)
			if err == io.EOF {
				break
			} else if err != nil {
				println("File read error:", err.Error())
				os.Exit(2)
			}

			fileWriterQueue <- block{ filePath, uint16(bytesRead), buffer, writeComplete }
			<-writeComplete
		}

		file.Close()
		workInProgress.Done()
	}
}

func fileWriter(fileWriterQueue <-chan block) {
	output, err := os.Create("test.output")
	if err != nil {
		println("File output write error:", err.Error())
		os.Exit(3)
	}

	for block := range fileWriterQueue {

		filePath := []byte(block.filePath)
		err = binary.Write(output, binary.BigEndian, uint32(len(filePath)))
		if err != nil {
			println("File output write error:", err.Error())
			os.Exit(3)
		}
		_, err = output.Write(filePath)
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

		block.writeComplete <- true

	}
}

