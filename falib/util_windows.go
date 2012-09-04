package falib

import "os"

func (a *Archiver) getModeOwnership(file *os.File) (uid int, gid int, mode os.FileMode) {
	fi, err := file.Stat()
	if err != nil {
		a.Logger.Warning("file stat error; uid/gid/mode will be incorrect:", err.Error())
	} else {
		mode = fi.Mode()
	}
	return
}
