package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Files kept only in the cloud (iCloud Drive, a File Provider) are flagged
// "dataless". The node looks for them and has them downloaded through
// Apple's own find, stat and head, never reading the folder itself: macOS
// lets them into a protected folder where it would hold eriphe-node back
// (apple_explore.go).

// cloudOnly counts the files of dir kept only in the cloud and their size.
func cloudOnly(dir string) (count int, size int64, err error) {
	out, err := exec.Command("/usr/bin/find", dir, "-type", "f", "-flags", "+dataless",
		"-exec", "/usr/bin/stat", "-f", "%z", "{}", "+").Output()
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		n, perr := strconv.ParseInt(strings.TrimSpace(sc.Text()), 10, 64)
		if perr == nil {
			count++
			size += n
		}
	}
	if err != nil && count == 0 && len(out) == 0 {
		// find fails on a folder it cannot open: nothing it could download either.
		return 0, 0, nil
	}
	return count, size, nil
}

// fetchCloud reads one byte of each file of dir kept only in the cloud,
// which has the system download it.
func fetchCloud(dir string) error {
	cmd := exec.Command("/usr/bin/find", dir, "-type", "f", "-flags", "+dataless",
		"-exec", "/usr/bin/head", "-c", "1", "{}", "+")
	cmd.Stdout = io.Discard
	cmd.Run()
	if left, _, _ := cloudOnly(dir); left > 0 {
		return fmt.Errorf("%d files are still only in the cloud (is this Mac online?)", left)
	}
	return nil
}
