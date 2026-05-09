package rss2masto

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

type hashDictData struct {
	dict map[string]string
}

// HashDictFile is the path to the external hash dictionary file used for hashtag translation.
// It maps raw strings extracted from item links (via HashLink regex) to their desired hashtag forms.
// Each line must be in the format: key=value (e.g. "krakow=Kraków" or "hokej-na-lodzie=HokejNaLodzie").
// Lines starting with '#' are treated as comments and ignored.
// The file is loaded once at startup via init() and can be reloaded at runtime with ReloadHashDict.
var HashDictFile = "./hashdict.txt"

var (
	// hashDictCache holds the loaded dictionary.
	hashDictCache atomic.Pointer[hashDictData]
	// reloadMu - Mutex for synchronizing dictionary reloads. Only used for writing (reloading), reads are lock-free.
	reloadMu sync.Mutex
)

func init() {
	ReloadHashDict(nil)
}

// ReloadHashDict replaces the hash dictionary with the provided data.
// The data format is the same as the file format: key=value lines,
// with '#' as comment prefix.
// If data is nil, the file at HashDictFile is read.
// Calling ReloadHashDict is safe for concurrent use — reads are always lock-free.
// Typical use: reload after updating hashdict.txt at runtime without restarting.
func ReloadHashDict(data []byte) {
	reloadMu.Lock()
	defer reloadMu.Unlock()

	newDict := parseHashDictFile(data)
	if len(newDict) == 0 {
		return
	}
	hashDictCache.Store(&hashDictData{dict: newDict})
}

// ViewHashDict returns a copy of the current dictionary, in file format.
// The result is sorted by key.
func ViewHashDict() []byte {
	data := hashDictCache.Load()
	if data == nil {
		return nil
	}

	keys := make([]string, 0, len(data.dict))
	for key := range data.dict {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&sb, "%s=%s\n", key, data.dict[key])
	}
	return s2b(sb.String())
}

// parseHashDictFile parses the hash dictionary.
// If data is non-nil, it is parsed directly; otherwise the file at HashDictFile is read.
func parseHashDictFile(data []byte) map[string]string {
	var scanner *bufio.Scanner

	if data != nil {
		scanner = bufio.NewScanner(bytes.NewReader(data))
	} else {
		f, err := os.Open(HashDictFile)
		if err != nil {
			fmt.Printf("hashDict: cannot open %s: %v\n", HashDictFile, err)
			return nil
		}
		defer f.Close()
		scanner = bufio.NewScanner(f)
	}

	newDict := make(map[string]string)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		newDict[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("hashDict: error reading %s: %v\n", HashDictFile, err)
		return nil
	}
	return newDict
}

// hashDict looks up a string in the hash dictionary.
// If the key is found, the mapped value is returned; otherwise the original string is returned unchanged.
// Used by makeHashtags to translate raw URL segments into proper hashtag forms
// (e.g. "hokej-na-lodzie" → "HokejNaLodzie", "krakow" → "Kraków").
func hashDict(s string) string {
	data := hashDictCache.Load()
	if data == nil {
		return s
	}
	val, ok := data.dict[s]
	if !ok {
		return s
	}
	return val
}
