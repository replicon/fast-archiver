fast-archiver file format
~~~~~~~~~~~~~~~~~~~~~~~~~

Header
------

fast-archiver files begin with an eight-byte header; the idea for this header
was liberated from the PNG spec, but "PNG" has been replaced with "FA1".  It's
expected that any radical changes to the file format would change the header
(eg. FA2, FA2...).

Header [8 bytes]: 0x89, 0x46, 0x41, 0x31, 0x0D, 0x0A, 0x1A, 0x0A


Blocks
------

Following that is an unlimited number of blocks.  Every block has this same
header (all values are in network byte-order / big-endian):

    uint16 -- size of file path in bytes

    byte[n] -- UTF-8 encoded file path

    byte -- block type identifier

The last byte is an identifier for the type of block:

    0 = data block

    1 = start file block

    2 = end file block

    3 = directory block

    4 = checksum block

Additional block types may be added in the future to support symlinks, or maybe
additional metadata like ACLs.

Data Block
==========

A data block is part of a file.  Data blocks in the archive will appear
sequentially in the order that the were read from the data file.  The format of
the block is:

    uint16 -- size of block

    byte[n] -- raw data

Start File
==========

This block indicates the beginning of a file.  All the data blocks for a file
will appear between the start file and end file blocks for that file, allowing
the archive extraction to know when to close and open the file.  The format is:

    uint32 -- UID of the file

    uint32 -- GID of the file

    uint32 -- Permission mode of the file

End File
========

This block indicates the end of a file.  There is no data in the block.

Directory
=========

The directory block contains information on a directory.  Directories are
always archived before the files contained in them.  The format of this block
is:

    uint32 -- UID of the directory

    uint32 -- GID of the directory

    uint32 -- Permission mode of the directory

Checksum
========

Checksum blocks appear arbitrarily in the archive format, and contain a CRC64
checksum of all the data in the file so far, including the file header,
including the checksum block type, and including the checksum value of any
previous checksum blocks.  The file path of the checksum block is zero bytes.
The checksum block just contains:

    uint64 -- CRC64 checksum

