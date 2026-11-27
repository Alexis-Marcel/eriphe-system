package main

import (
	"database/sql"
	"strings"
	"time"
)

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// foldAccents lowers and strips the accents of Latin letters, like the FTS tokenizer.
func foldAccents(s string) string {
	r := strings.NewReplacer("à", "a", "â", "a", "ä", "a", "á", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
		"î", "i", "ï", "i", "í", "i", "ô", "o", "ö", "o", "ó", "o", "ù", "u", "û", "u", "ü", "u", "ú", "u", "ÿ", "y", "ñ", "n",
		"œ", "oe", "æ", "ae")
	return r.Replace(strings.ToLower(s))
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// Times are unix seconds; a zero time is NULL.
func unixOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func fromNull(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0)
}

type querier interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}
