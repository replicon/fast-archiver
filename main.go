package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type block struct {
	filePath    string
	numBytes    uint16
	buffer      []byte
	startOfFile bool
	endOfFile   bool
}

var blockSize uint16
var verbose bool
var logger *log.Logger

const dataBlockFlag byte = 1 << 0
const startOfFileFlag byte = 1 << 1
const endOfFileFlag byte = 1 << 2

func main() {
	extract := flag.Bool("x", false, "extract archive")
	create := flag.Bool("c", false, "create archive")
	inputFileName := flag.String("i", "", "input file for extraction; defaults to stdin (-x only)")
	outputFileName := flag.String("o", "", "output file for creation; defaults to stdout (-c only)")
	requestedBlockSize := flag.Uint("block-size", 4096, "internal block-size (-c only)")
	dirReaderCount := flag.Int("dir-readers", 16, "number of simultaneous directory readers (-c only)")
	fileReaderCount := flag.Int("file-readers", 16, "number of simultaneous file readers (-c only)")
	directoryScanQueueSize := flag.Int("queue-dir", 128, "queue size for scanning directories (-c only)")
	fileReadQueueSize := flag.Int("queue-read", 128, "queue size for reading files (-c only)")
	fileWriteQueueSize := flag.Int("queue-write", 128, "queue size for archive write (-c only); increasing can cause increased memory usage")
	multiCpu := flag.Int("multicpu", 1, "maximum number of CPUs that can be executing simultaneously")

	flag.BoolVar(&verbose, "v", false, "verbose output on stderr")
	flag.Parse()

	runtime.GOMAXPROCS(*multiCpu)

	logger = log.New(os.Stderr, "", 0)

	if *requestedBlockSize > math.MaxUint16 {
		logger.Fatalln("block-size must be less than or equal to", math.MaxUint16)
	}
	blockSize = uint16(*requestedBlockSize)

	if *extract {
		var inputFile *os.File
		if *inputFileName != "" {
			file, err := os.Open(*inputFileName)
			if err != nil {
				logger.Fatalln("Error opening input file:", err.Error())
			}
			inputFile = file
		} else {
			inputFile = os.Stdin
		}

		bufferedInputFile := bufio.NewReader(inputFile)
		archiveReader(bufferedInputFile)
		inputFile.Close()

	} else if *create {
		if flag.NArg() == 0 {
			logger.Fatalln("Directories to archive must be specified")
		}

		var directoryScanQueue = make(chan string, *directoryScanQueueSize)
		var fileReadQueue = make(chan string, *fileReadQueueSize)
		var fileWriteQueue = make(chan block, *fileWriteQueueSize)
		var workInProgress sync.WaitGroup

		var outputFile *os.File
		if *outputFileName != "" {
			file, err := os.Create(*outputFileName)
			if err != nil {
				logger.Fatalln("Error creating output file:", err.Error())
			}
			outputFile = file
		} else {
			outputFile = os.Stdout
		}

		bufferedOutputFile := bufio.NewWriter(outputFile)
		go archiveWriter(bufferedOutputFile, fileWriteQueue, &workInProgress)
		for i := 0; i < *dirReaderCount; i++ {
			go directoryScanner(directoryScanQueue, fileReadQueue, &workInProgress)
		}
		for i := 0; i < *fileReaderCount; i++ {
			go fileReader(fileReadQueue, fileWriteQueue, &workInProgress)
		}

		for i := 0; i < flag.NArg(); i++ {
			workInProgress.Add(1)
			directoryScanQueue <- flag.Arg(i)
		}

		workInProgress.Wait()
		close(directoryScanQueue)
		close(fileReadQueue)
		close(fileWriteQueue)
		bufferedOutputFile.Flush()
		outputFile.Close()
	} else {
		logger.Fatalln("extract (-x) or create (-c) flag must be provided")
	}
}

func directoryScanner(directoryScanQueue chan string, fileReadQueue chan string, workInProgress *sync.WaitGroup) {
	for directoryPath := range directoryScanQueue {
		if verbose {
			logger.Println(directoryPath)
		}

		files, err := ioutil.ReadDir(directoryPath)
		if err == nil {
			workInProgress.Add(len(files))
			for _, file := range files {
				filePath := filepath.Join(directoryPath, file.Name())
				if file.IsDir() {
					directoryScanQueue <- filePath
				} else {
					fileReadQueue <- filePath
				}
			}
		} else {
			logger.Println("directory read error:", err.Error())
		}

		workInProgress.Done()
	}
}

func fileReader(fileReadQueue <-chan string, fileWriterQueue chan block, workInProgress *sync.WaitGroup) {
	for filePath := range fileReadQueue {
		if verbose {
			logger.Println(filePath)
		}

		file, err := os.Open(filePath)
		if err == nil {
			workInProgress.Add(1)
			fileWriterQueue <- block{filePath, 0, nil, true, false}

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
				fileWriterQueue <- block{filePath, uint16(bytesRead), buffer, false, false}
			}

			workInProgress.Add(1)
			fileWriterQueue <- block{filePath, 0, nil, false, true}

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

		if block.startOfFile {
			flags[0] = startOfFileFlag
			_, err = output.Write(flags)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
		} else if block.endOfFile {
			flags[0] = endOfFileFlag
			_, err = output.Write(flags)
			if err != nil {
				logger.Fatalln("Archive write error:", err.Error())
			}
		} else {
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
		}

		workInProgress.Done()
	}
}

func archiveReader(file io.Reader) {
	var workInProgress sync.WaitGroup
	fileOutputChan := make(map[string]chan block)

	for {
		var pathSize uint16
		err := binary.Read(file, binary.BigEndian, &pathSize)
		if err == io.EOF {
			break
		} else if err != nil {
			logger.Fatalln("Archive read error:", err.Error())
		}

		buf := make([]byte, pathSize)
		_, err = io.ReadFull(file, buf)
		if err != nil {
			logger.Fatalln("Archive read error:", err.Error())
		}
		filePath := string(buf)

		flag := make([]byte, 1)
		_, err = io.ReadFull(file, flag)
		if err != nil {
			logger.Fatalln("Archive read error:", err.Error())
		}

		if flag[0] == startOfFileFlag {
			c := make(chan block, 1)
			fileOutputChan[filePath] = c
			workInProgress.Add(1)
			go writeFile(c, &workInProgress)
			c <- block{filePath, 0, nil, true, false}
		} else if flag[0] == endOfFileFlag {
			c := fileOutputChan[filePath]
			c <- block{filePath, 0, nil, false, true}
			close(c)
			delete(fileOutputChan, filePath)
		} else if flag[0] == dataBlockFlag {
			var blockSize uint16
			err = binary.Read(file, binary.BigEndian, &blockSize)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}

			blockData := make([]byte, blockSize)
			_, err = io.ReadFull(file, blockData)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}

			c := fileOutputChan[filePath]
			c <- block{filePath, blockSize, blockData, false, false}
		} else {
			logger.Fatalln("Archive error: unrecognized block flag", flag[0])
		}
	}

	workInProgress.Wait()
}

func writeFile(blockSource chan block, workInProgress *sync.WaitGroup) {
	var file *os.File = nil
	var bufferedFile *bufio.Writer
	for block := range blockSource {
		if block.startOfFile {
			if verbose {
				logger.Println(block.filePath)
			}

			dir, _ := filepath.Split(block.filePath)
			err := os.MkdirAll(dir, os.ModeDir|0755)
			if err != nil {
				logger.Fatalln("Directory create error:", err.Error())
			}

			tmp, err := os.Create(block.filePath)
			if err != nil {
				logger.Fatalln("File create error:", err.Error())
			}
			file = tmp
			bufferedFile = bufio.NewWriter(file)
		} else if block.endOfFile {
			bufferedFile.Flush()
			file.Close()
			file = nil
		} else {
			_, err := bufferedFile.Write(block.buffer[:block.numBytes])
			if err != nil {
				logger.Fatalln("File write error:", err.Error())
			}
		}
	}
	workInProgress.Done()
}
