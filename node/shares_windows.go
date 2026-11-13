package main

import (
	"io/fs"
	"os"
)

// eriphe-node runs on Linux (in WSL on Windows): no owners to read here.

func ownerGID(string) int { return -1 }

func writableByUser(string) bool { return true }

// keepOwner: no owners here.
func keepOwner(string, fs.FileInfo) {}

// keepOwnerIn: no owners here.
func keepOwnerIn(*os.Root, string, fs.FileInfo) {}
