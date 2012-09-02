package falib

import "os"

type blockType byte

const (
	blockTypeData blockType = iota
	blockTypeStartOfFile
	blockTypeEndOfFile
	blockTypeDirectory
	blockTypeChecksum
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

// Archive header: stole ideas from the PNG file header here, but replaced
// 'PNG' with 'FA1' to identify the fast-archive format (version 1).
var fastArchiverHeader = []byte{0x89, 0x46, 0x41, 0x31, 0x0D, 0x0A, 0x1A, 0x0A}
