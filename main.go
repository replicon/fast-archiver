package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/replicon/fast-archiver/falib"
	"log"
	"math"
	"os"
	"runtime"
	"path/filepath"
)

var logger *log.Logger
var tag string
var rev string

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s (tag: %s, rev: %s)\n", os.Args[0], tag, rev)
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
	}

	extract := flag.Bool("x", false, "extract archive")
	create := flag.Bool("c", false, "create archive")
	inputFileName := flag.String("i", "", "input file for extraction; defaults to stdin (-x only)")
	outputFileName := flag.String("o", "", "output file for creation; defaults to stdout (-c only)")
	requestedBlockSize := flag.Uint("block-size", 4096, "internal block-size (-c only)")
	dirReaderCount := flag.Int("dir-readers", 16, "number of simultaneous directory readers (-c only)")
	fileReaderCount := flag.Int("file-readers", 16, "number of simultaneous file readers (-c only)")
	directoryScanQueueSize := flag.Int("queue-dir", 128, "queue size for scanning directories (-c only)")
	fileReadQueueSize := flag.Int("queue-read", 128, "queue size for reading files (-c only)")
	blockQueueSize := flag.Int("queue-write", 128, "queue size for archive write (-c only); increasing can cause increased memory usage")
	multiCpu := flag.Int("multicpu", 1, "maximum number of CPUs that can be executing simultaneously")
	exclude := flag.String("exclude", "", "file patterns to exclude (eg. core.*); can be path list separated (eg. : in Linux) for multiple excludes (-c only)")
	flag.BoolVar(&falib.Verbose, "v", false, "verbose output on stderr")
	flag.BoolVar(&falib.IgnorePerms, "ignore-perms", false, "ignore permissions when restoring files (-x only)")
	flag.BoolVar(&falib.IgnoreOwners, "ignore-owners", false, "ignore owners when restoring files (-x only)")
	flag.Parse()

	runtime.GOMAXPROCS(*multiCpu)

	logger := log.New(os.Stderr, "", 0)
	falib.Logger = logger

	if *requestedBlockSize > math.MaxUint16 {
		logger.Fatalln("block-size must be less than or equal to", math.MaxUint16)
	}
	falib.BlockSize = uint16(*requestedBlockSize)

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
		falib.ArchiveReader(bufferedInputFile)
		inputFile.Close()

	} else if *create {
		if flag.NArg() == 0 {
			logger.Fatalln("Directories to archive must be specified")
		}

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

		var archiver = falib.NewArchiver(outputFile)
		archiver.DirScanQueueSize = *directoryScanQueueSize
		archiver.FileReadQueueSize = *fileReadQueueSize
		archiver.BlockQueueSize = *blockQueueSize
		archiver.ExcludePatterns = filepath.SplitList(*exclude)
		archiver.DirReaderCount = *dirReaderCount
		archiver.FileReaderCount = *fileReaderCount
		for i := 0; i < flag.NArg(); i++ {
			archiver.AddDir(flag.Arg(i))
		}
		archiver.Run()
		outputFile.Close()
	} else {
		logger.Fatalln("extract (-x) or create (-c) flag must be provided")
	}
}
