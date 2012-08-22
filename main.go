package main

import (
	"encoding/binary"
	"fmt"
	"time"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

type block struct {
	filePath      string
	numBytes      uint16
	buffer        []byte
	writeComplete chan bool
}

func main() {
	var directoryScanQueue = make(chan string, 128)
	var fileReadQueue = make(chan string, 128)
	var fileWriteQueue = make(chan block, 1)

	go fileWriter(fileWriteQueue)

	for i := 0; i < 16; i++ {
		go directoryScanner(directoryScanQueue, fileReadQueue)
	}
	for i := 0; i < 16; i++ {
		go fileReader(fileReadQueue, fileWriteQueue)
	}

	directoryScanQueue <- "test"

    fmt.Printf("hello, world\n")
	time.Sleep(time.Second * 5)
    fmt.Printf("goodbye, world\n")
}

func directoryScanner(directoryScanQueue chan string, fileReadQueue chan string) {
	for directoryPath := range directoryScanQueue {
		files, err := ioutil.ReadDir(directoryPath)
		if err != nil {
			println("Directory read error:", err.Error())
			os.Exit(1)
		}

		for _, file := range files {
			filePath := filepath.Join(directoryPath, file.Name())
			if file.IsDir() {
				directoryScanQueue <- filePath
			} else {
				fileReadQueue <- filePath
			}
		}
	}
}

func fileReader(fileReadQueue <-chan string, fileWriterQueue chan block) {
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

