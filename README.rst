fast-archiver
~~~~~~~~~~~~~

fast-archiver is a command-line tool for archiving directories, and restoring
those archives.

The "fast" part of the archiver is two-fold:

1. fast-archiver reads a number of files concurrently and then serializes
   the output, allowing it to have higher throughput on large numbers
   of small files, and

2. it begins archiving files before it has completed reading the directory
   entries that it is archiving, allowing for a fast startup time
   compared to tools that first create an inventory of files to
   transfer.

For example, this tool was tested on a PostgreSQL server with a 90 gigabyte
database.  The pg_data directory contained a total of 2,089,214 files, the
majority of which were 8kiB to 24kiB in size.  fast-archiver piped over ssh
could transfer this database from one machine to another in 1 hour, 30 minutes;
rsync took over 3 hours.  Even though rsync could avoid copying files that had
matching sizes and timestamps, fast-archiver was still faster because it went
straight for high-I/O instead.


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

fast-archiver is written in `Go`_, for Go version 1.  To build it, run ``go
build`` in the fast-archiver directory.

.. _Go: http://golang.org/


Command-line arguments
----------------------

+------------------+-------------------------------------------------------------------------------+
| -x               | Extract archive mode.                                                         |
+------------------+-------------------------------------------------------------------------------+
| -c               | Create archive mode.                                                          |
+------------------+-------------------------------------------------------------------------------+
| --multicpu       | Allows concurrent activities to run on the specified number of CPUs.  Since   |
|                  | the archiving is dominated by I/O, additional CPUs tend to just add overhead  |
|                  | in communicating between concurrent processes, but it could increase          |
|                  | throughput in some scenarios.  Defaults to 1.                                 |
+------------------+-------------------------------------------------------------------------------+


Create-mode only
================

+------------------+-----------------------------------------------------------------------------------------+
| -o               | Output path for the archive.  Defaults to stdout.                                       |
+------------------+-----------------------------------------------------------------------------------------+
| --exclude        | A colon-separated list of paths to exclude from the archive.  Can include               |
|                  | wildcards and other shell matching constructs.                                          |
+------------------+-----------------------------------------------------------------------------------------+
| --block-size     | Specifies the size of blocks being read from disk, in bytes.  The larger                |
|                  | the block size, the more memory fast-archiver will use, but it could result             |
|                  | in higher I/O rates.  Defaults to 4096, maximum value is 65535.                         |
+------------------+-----------------------------------------------------------------------------------------+
| --dir-readers    | The maximum number of directories that will be read concurrently.  Defaults to 16.      |
+------------------+-----------------------------------------------------------------------------------------+
| --file-readers   | The maximum number of files that will be read concurrently.  Defaults to 16.            |
+------------------+-----------------------------------------------------------------------------------------+
| --queue-dir      | The maximum size of the queue for sub-directory paths to be processed. Defaults to 128. |
+------------------+-----------------------------------------------------------------------------------------+
| --queue-read     | The maximum size of the queue for file paths to be processed.  Defaults to 128.         |
+------------------+-----------------------------------------------------------------------------------------+
| --queue-write    | The maximum size of the block queue for archive output.  Increasing this will increase  |
|                  | the potential memory usage, as (queue-write * block-size) memory could be allocated for |
|                  | file reads.  Defaults to 128.                                                           |
+------------------+-----------------------------------------------------------------------------------------+


Extract-mode only
=================

+-----------------+-----------------------------------------------------------------------+
| -i              | Input path for the archive.  Defaults to stdin.                       |
+-----------------+-----------------------------------------------------------------------+
| --ignore-perms  | Do not restore permissions on files and directories.                  |
+-----------------+-----------------------------------------------------------------------+
| --ignore-owners | Do not restore uid and gid on files and directories.                  |
+-----------------+-----------------------------------------------------------------------+

