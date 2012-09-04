fast-archiver
~~~~~~~~~~~~~

fast-archiver is a command-line tool for archiving directories, and restoring
those archives.

fast-archiver uses a few techniques to try to be more efficient than
traditional tools:

1. It reads a number of files concurrently and then serializes the output.
   Most other tools use sequential file processing, where operations like
   ``open()``, ``lstat()``, and ``close()`` can cause a lot of overhead when
   reading huge numbers of small files.  Making these operations concurrent
   means that the tool is more often reading and writing data than you would
   be otherwise.

2. It begins archiving files before it has completed reading the directory
   entries that it is archiving, allowing for a fast startup time
   compared to tools that first create an inventory of files to
   transfer.

How Fast?
---------

On a test workload of 2,089,214 files representing a total of 90 GiB of data,
fast-archiver was compared with tar and rsync for reading data files and
transfering them over a network.  The test scenario was a PostgreSQL
database, with many of the files being small, 8-24kiB in size.

Compared with tar, fast-archiver took 33% of the execution time (27m 38s vs.
1h 23m 23s) to read the test workload and output the archive to /dev/null.
The tar output had to be redirected through cat to create a comparable
scenario, because tar recognized /dev/null and shortcuts the actual data file
reading and writing.  Here's the raw timing output for some hard data::

    $ time fast-archiver -c -o /dev/null /db/data
    skipping symbolic link /db/data/pg_xlog
    1008.92user 663.00system 27:38.27elapsed 100%CPU (0avgtext+0avgdata 24352maxresident)k
    0inputs+0outputs (0major+1732minor)pagefaults 0swaps
    
    $ time tar -cf - /db/data | cat > /dev/null
    tar: Removing leading `/' from member names
    tar: /db/data/base/16408/12445.2: file changed as we read it
    tar: /db/data/base/16408/12464: file changed as we read it
    32.68user 375.19system 1:23:23elapsed 8%CPU (0avgtext+0avgdata 81744maxresident)k
    0inputs+0outputs (0major+5163minor)pagefaults 0swaps

Compared with rsync, fast-archiver piped over ssh can transfer the database
from one machine to another in 1h 30m, vs. rsync in 3h.

These huge reductions in time may not be typical, but they happen to be the
workload that fast-archiver was designed for.

Examples
--------

Creates an archive (-c) reading the directory target1, and redirects the
archive to the file named target1.fast-archive::

    fast-archiver -c target1 > target1.fast-archive
    fast-archiver -c -o target1.fast-archive target1

Extracts the archive target1.fast-archive into the current directory::

    fast-archiver -x < target1.fast-archive
    fast-archiver -x -i target1.fast-archive

Creates a fast-archive remotely, and restores it locally, piping the data
through ssh::

    ssh postgres@10.32.32.32 "cd /db; fast-archive -c data --exclude=data/\*.pid" | fast-archiver -x


Build
-----

fast-archiver is written in `Go`_, for Go version 1.

The fast-archiver repository contains both a command-line tool (at the root)
and a package called ``falib`` which contains the archive reading and writing
code.  To make the build work correctly with both the library and the
command-line tool, it's necessary to setup the correct GOPATH and directory
references.

Here's a quick set of steps to setup the build:

1. Create a containing folder, eg. ``~/go-projects``

2. ``mkdir -p ~/go-projects/src/github.com/replicon/``

3. ``cd ~/go-projects/src/github.com/replicon/``

4. ``git clone https://github.com/replicon/fast-archiver.git``

5. ``cd fast-archiver``

6. ``GOPATH=~/go-projects ./build.sh``  (or use ``go build``, but you won't
   get version information in the built executable).

.. _Go: http://golang.org/


Command-line arguments
----------------------


-x
    Extract archive mode.

-c
    Create archive mode.

--multicpu
    Allows concurrent activities to run on the specified number of CPUs.  Since
    the archiving is dominated by I/O, additional CPUs tend to just add
    overhead in communicating between concurrent processes, but it could
    increase throughput in some scenarios.  Defaults to 1.


Create-mode only
================

-o
    Output path for the archive.  Defaults to stdout.

--exclude
    A colon-separated list of paths to exclude from the archive.  Can include
    wildcards and other shell matching constructs.

--block-size
    Specifies the size of blocks being read from disk, in bytes.  The larger
    the block size, the more memory fast-archiver will use, but it could result
    in higher I/O rates.  Defaults to 4096, maximum value is 65535.

--dir-readers
    The maximum number of directories that will be read concurrently.  Defaults
    to 16.

--file-readers
    The maximum number of files that will be read concurrently.  Defaults to
    16.

--queue-dir
    The maximum size of the queue for sub-directory paths to be processed.
    Defaults to 128.

--queue-read
    The maximum size of the queue for file paths to be processed.  Defaults to
    128.

--queue-write
    The maximum size of the block queue for archive output.  Increasing this
    will increase the potential memory usage, as (queue-write * block-size)
    memory could be allocated for file reads.  Defaults to 128.


Extract-mode only
=================

-i
    Input path for the archive.  Defaults to stdin.

--ignore-perms
    Do not restore permissions on files and directories.

--ignore-owners
    Do not restore uid and gid on files and directories.

