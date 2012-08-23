package main

import (
	"bufio"
	"flag"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type blockType int

const (
	blockTypeData = iota
	blockTypeStartOfFile
	blockTypeEndOfFile
	blockTypeDirectory
)

type block struct {
	filePath  string
	numBytes  uint16
	buffer    []byte
	blockType blockType
	uid       int
	gid       int
	mode      os.FileMode
}

var blockSize uint16
var verbose bool
var logger *log.Logger
var ignorePerms bool
var ignoreOwners bool

const dataBlockFlag byte = 1 << 0
const startOfFileFlag byte = 1 << 1
const endOfFileFlag byte = 1 << 2
const directoryFlag byte = 1 << 3

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
	exclude := flag.String("exclude", "", "file patterns to exclude (eg. core.*); can be path list separated (eg. : in Linux) for multiple excludes (-c only)")
	flag.BoolVar(&verbose, "v", false, "verbose output on stderr")
	flag.BoolVar(&ignorePerms, "ignore-perms", false, "ignore permissions when restoring files (-x only)")
	flag.BoolVar(&ignoreOwners, "ignore-owners", false, "ignore owners when restoring files (-x only)")
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
		var excludes = filepath.SplitList(*exclude)

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
			go directoryScanner(directoryScanQueue, fileReadQueue, fileWriteQueue, excludes, &workInProgress)
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
