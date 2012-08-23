package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
)

type blockType int
const (
	blockTypeData = iota
	blockTypeStartOfFile
	blockTypeEndOfFile
	blockTypeDirectory
)


type block struct {
	filePath    string
	numBytes    uint16
	buffer      []byte
	blockType   blockType
	uid         int
	gid         int
	mode        os.FileMode
}

var blockSize uint16
var verbose bool
var logger *log.Logger

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
			var uid uint32
			var gid uint32
			var mode os.FileMode

			err = binary.Read(file, binary.BigEndian, &uid)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}

			err = binary.Read(file, binary.BigEndian, &gid)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}

			err = binary.Read(file, binary.BigEndian, &mode)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}

			c := make(chan block, 1)
			fileOutputChan[filePath] = c
			workInProgress.Add(1)
			go writeFile(c, &workInProgress)
			c <- block{filePath, 0, nil, blockTypeStartOfFile, int(uid), int(gid), mode}
		} else if flag[0] == endOfFileFlag {
			c := fileOutputChan[filePath]
			c <- block{filePath, 0, nil, blockTypeEndOfFile, 0, 0, 0}
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
			c <- block{filePath, blockSize, blockData, blockTypeData, 0, 0, 0}
		} else if flag[0] == directoryFlag {
			var uid uint32
			var gid uint32
			var mode os.FileMode

			err = binary.Read(file, binary.BigEndian, &uid)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}
			err = binary.Read(file, binary.BigEndian, &gid)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}
			err = binary.Read(file, binary.BigEndian, &mode)
			if err != nil {
				logger.Fatalln("Archive read error:", err.Error())
			}

			err = os.Mkdir(filePath, mode)
			if err != nil && !os.IsExist(err) {
				logger.Fatalln("Directory create error:", err.Error())
			}
			err = os.Chown(filePath, int(uid), int(gid))
			if err != nil {
				logger.Println("Directory chown error:", err.Error())
			}


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
		if block.blockType == blockTypeStartOfFile {
			if verbose {
				logger.Println(block.filePath)
			}

			tmp, err := os.Create(block.filePath)
			if err != nil {
				logger.Fatalln("File create error:", err.Error())
			}
			file = tmp
			bufferedFile = bufio.NewWriter(file)

			err = file.Chown(block.uid, block.gid)
			if err != nil {
				logger.Println("Unable to chown file to", block.uid, "/", block.gid, ":", err.Error())
			}
			err = file.Chmod(block.mode)
			if err != nil {
				logger.Println("Unable to chmod file to", block.mode, ":", err.Error())
			}
		} else if block.blockType == blockTypeEndOfFile {
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
