package main

import "io/fs"

// eriphe-node runs on Linux (in WSL on Windows): no owners to read here.

func ownerGID(string) int { return -1 }

func writableByTasks(fi fs.FileInfo) bool { return fi.Mode().Perm()&0o200 != 0 }
