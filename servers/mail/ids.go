package main

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// msgRef locates a message: account, folder, the folder's UIDVALIDITY and the
// message UID. Its string form is the opaque id the tools hand out.
type msgRef struct {
	Account     string
	Folder      string
	UIDValidity uint32
	UID         imap.UID
}

const idSep = "\x1f"

func (r msgRef) String() string {
	s := strings.Join([]string{"m1", r.Account, r.Folder,
		strconv.FormatUint(uint64(r.UIDValidity), 10), strconv.FormatUint(uint64(r.UID), 10)}, idSep)
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

var errBadID = errors.New("invalid message id: use an id returned by search_messages, read_message or create_draft")

func parseRef(id string) (msgRef, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(id))
	if err != nil {
		return msgRef{}, errBadID
	}
	p := strings.Split(string(b), idSep)
	if len(p) != 5 || p[0] != "m1" {
		return msgRef{}, errBadID
	}
	v, err1 := strconv.ParseUint(p[3], 10, 32)
	u, err2 := strconv.ParseUint(p[4], 10, 32)
	if err1 != nil || err2 != nil || u == 0 {
		return msgRef{}, errBadID
	}
	return msgRef{p[1], p[2], uint32(v), imap.UID(u)}, nil
}

// folderKey groups references by mailbox.
type folderKey struct {
	Account, Folder string
	UIDValidity     uint32
}

func groupRefs(ids []string) (map[folderKey][]imap.UID, []folderKey, error) {
	groups := map[folderKey][]imap.UID{}
	var order []folderKey
	for _, id := range ids {
		r, err := parseRef(id)
		if err != nil {
			return nil, nil, err
		}
		k := folderKey{r.Account, r.Folder, r.UIDValidity}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r.UID)
	}
	return groups, order, nil
}
