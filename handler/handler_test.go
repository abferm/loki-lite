package handler

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/abferm/loki-lite/engine"
	"github.com/abferm/loki-lite/journal"
	"github.com/abferm/loki-lite/model"
	"github.com/abferm/loki-lite/util"
)

type testEntry struct {
	seqnum   uint64
	realtime uint64
	fields   map[string]string
}

func writeTestJournal(t *testing.T, dir, name string, entries []testEntry) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	if len(entries) == 0 {
		t.Fatal("writeTestJournal: must have at least one entry")
	}

	var headSeqnum, tailSeqnum uint64
	var headRealtime, tailRealtime uint64
	headSeqnum = entries[0].seqnum
	tailSeqnum = entries[0].seqnum
	headRealtime = entries[0].realtime
	tailRealtime = entries[0].realtime
	for _, e := range entries[1:] {
		if e.seqnum < headSeqnum {
			headSeqnum = e.seqnum
		}
		if e.seqnum > tailSeqnum {
			tailSeqnum = e.seqnum
		}
		if e.realtime < headRealtime {
			headRealtime = e.realtime
		}
		if e.realtime > tailRealtime {
			tailRealtime = e.realtime
		}
	}

	const headerSize uint64 = 240

	fieldMap := make(map[string][]uint64)
	fileID := [16]byte{}

	var content []byte
	content = append(content, make([]byte, headerSize)...)
	copy(content[0:8], []byte("LPKSHHRH"))

	entryOffsets := make([]uint64, 0, len(entries))
	for _, e := range entries {
		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}

		type entryItem struct {
			dataOffset uint64
		}
		var items []entryItem
		for key, val := range e.fields {
			payload := []byte(key + "=" + val)
			const dataObjHeader = 16 + 8 + 8 + 8 + 8 + 8 + 8
			dObjSize := uint64(dataObjHeader) + uint64(len(payload))
			dObj := make([]byte, dObjSize)
			dObj[0] = 1
			binary.LittleEndian.PutUint64(dObj[8:16], dObjSize)
			copy(dObj[dataObjHeader:], payload)

			if len(content)%8 != 0 {
				content = append(content, make([]byte, 8-len(content)%8)...)
			}
			dOff := uint64(len(content))
			content = append(content, dObj...)

			items = append(items, entryItem{dataOffset: dOff})
			fieldMap[key] = append(fieldMap[key], dOff)
		}

		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}

		entryObjSize := uint64(64) + uint64(len(items)*16)
		entryObj := make([]byte, entryObjSize)
		entryObj[0] = 3
		binary.LittleEndian.PutUint64(entryObj[8:16], entryObjSize)
		binary.LittleEndian.PutUint64(entryObj[16:24], e.seqnum)
		binary.LittleEndian.PutUint64(entryObj[24:32], e.realtime)

		for i, item := range items {
			itemOff := 64 + i*16
			binary.LittleEndian.PutUint64(entryObj[itemOff:itemOff+8], item.dataOffset)
		}

		entryOff := uint64(len(content))
		content = append(content, entryObj...)
		entryOffsets = append(entryOffsets, entryOff)
	}

	for _, offsets := range fieldMap {
		for i := 0; i < len(offsets)-1; i++ {
			nextFieldFileOff := offsets[i] + 32
			if nextFieldFileOff+8 <= uint64(len(content)) {
				binary.LittleEndian.PutUint64(content[nextFieldFileOff:nextFieldFileOff+8], offsets[i+1])
			}
		}
	}

	fieldNames := make([]string, 0, len(fieldMap))
	for name := range fieldMap {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)

	fieldObjOffsets := make(map[string]uint64)
	for _, name := range fieldNames {
		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}
		fieldObjOffsets[name] = uint64(len(content))

		payload := []byte(name + "\x00")
		foSize := uint64(16 + 8 + 8 + 8 + len(payload))
		fo := make([]byte, foSize)
		fo[0] = 2
		binary.LittleEndian.PutUint64(fo[8:16], foSize)
		binary.LittleEndian.PutUint64(fo[16:24], journal.SipHash24(fileID, []byte(name)))
		if len(fieldMap[name]) > 0 {
			binary.LittleEndian.PutUint64(fo[32:40], fieldMap[name][0])
		}
		copy(fo[40:], payload)
		content = append(content, fo...)
	}

	fieldTable, fieldTableSize := buildFieldHashTable(fieldNames, fileID, fieldObjOffsets, content)
	fieldTableAbsOff := uint64(len(content))
	content = append(content, fieldTable...)

	binary.LittleEndian.PutUint64(content[88:96], headerSize)
	binary.LittleEndian.PutUint64(content[120:128], fieldTableAbsOff)
	binary.LittleEndian.PutUint64(content[128:136], fieldTableSize)
	binary.LittleEndian.PutUint64(content[136:144], entryOffsets[len(entryOffsets)-1])
	binary.LittleEndian.PutUint64(content[144:152], uint64(len(entries)+len(fieldNames)))
	binary.LittleEndian.PutUint64(content[152:160], uint64(len(entries)))
	binary.LittleEndian.PutUint64(content[160:168], tailSeqnum)
	binary.LittleEndian.PutUint64(content[168:176], headSeqnum)
	binary.LittleEndian.PutUint64(content[184:192], headRealtime)
	binary.LittleEndian.PutUint64(content[192:200], tailRealtime)
	binary.LittleEndian.PutUint64(content[216:224], uint64(len(fieldNames)))
	content[16] = 1

	if _, err := f.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
}

func buildFieldHashTable(fieldNames []string, fileID [16]byte, fieldObjOffsets map[string]uint64, content []byte) ([]byte, uint64) {
	if len(fieldNames) == 0 {
		return nil, 0
	}

	numSlots := uint64(1)
	for numSlots < uint64(len(fieldNames)) {
		numSlots *= 2
	}

	tableSize := numSlots * 16
	table := make([]byte, tableSize)

	for _, name := range fieldNames {
		hash := journal.SipHash24(fileID, []byte(name))
		slot := hash % numSlots
		off := slot * 16

		foff := fieldObjOffsets[name]

		if binary.LittleEndian.Uint64(table[off:off+8]) == 0 {
			binary.LittleEndian.PutUint64(table[off:off+8], foff)
			binary.LittleEndian.PutUint64(table[off+8:off+16], foff)
		} else {
			tailOff := binary.LittleEndian.Uint64(table[off+8 : off+16])
			nextHashContentOff := tailOff + 24
			binary.LittleEndian.PutUint64(content[nextHashContentOff:nextHashContentOff+8], foff)
			binary.LittleEndian.PutUint64(table[off+8:off+16], foff)
		}
	}

	return table, tableSize
}

func setupTestHandler(t *testing.T, entries []testEntry) (*Handler, *engine.Engine) {
	t.Helper()
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", entries)

	pool := util.NewPool(5, func() *journal.Journal {
		j, err := journal.OpenJournal(dir, "test")
		if err != nil {
			panic(fmt.Sprintf("setupTestHandler: %v", err))
		}
		return j
	}, func(j *journal.Journal) { j.Close() })
	t.Cleanup(func() { pool.Close() })

	schema := model.NewSchema([]string{})
	eng := engine.New(pool, &schema)
	return New(eng), eng
}

func TestReady(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "ready\n" {
		t.Fatalf("expected 'ready\\n', got %q", w.Body.String())
	}
}

func TestLabels(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "PRIORITY": "4", "MESSAGE": "hello"}},
	})

	pool := util.NewPool(5, func() *journal.Journal {
		j, err := journal.OpenJournal(dir, "test")
		if err != nil {
			panic(fmt.Sprintf("TestLabels: %v", err))
		}
		return j
	}, func(j *journal.Journal) { j.Close() })
	t.Cleanup(func() { pool.Close() })

	schema := model.NewSchema([]string{})
	eng := engine.New(pool, &schema)
	h := New(eng)

	req := httptest.NewRequest("GET", "/loki/api/v1/labels", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	sort.Strings(resp.Data)
	expected := []string{"job", "priority"}
	if len(resp.Data) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, resp.Data)
	}
	for i := range expected {
		if resp.Data[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, resp.Data)
		}
	}
}

func TestLabelValues(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "nginx", "MESSAGE": "world"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/label/job/values", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	sort.Strings(resp.Data)
	expected := []string{"nginx", "sshd"}
	if len(resp.Data) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, resp.Data)
	}
	for i := range expected {
		if resp.Data[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, resp.Data)
		}
	}
}

func TestLabelValuesExcluded(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/label/MESSAGE/values", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestQueryRangeLogQuery(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "nginx", "MESSAGE": "world"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query_range?query="+`{job="sshd"}`+"&start=0&end=10", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	if resp.Data.ResultType != "streams" {
		t.Fatalf("expected resultType streams, got %q", resp.Data.ResultType)
	}
}

func TestQueryRangeMetricQuery(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "world"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query_range?query="+`count_over_time({job="sshd"}[1s])`+"&start=0&end=3&step=1s", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	if resp.Data.ResultType != "matrix" {
		t.Fatalf("expected resultType matrix, got %q", resp.Data.ResultType)
	}
}

func TestQueryRangeMissingQuery(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query_range", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestQueryRangeInvalidQuery(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query_range?query=not+valid&start=0&end=10", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestQueryInstantMetric(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "world"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query?query="+`count_over_time({job="sshd"}[5s])`+"&time=3", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	if resp.Data.ResultType != "vector" {
		t.Fatalf("expected resultType vector, got %q", resp.Data.ResultType)
	}
}

func TestQueryInstantMissingQuery(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestFormatQuery(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/format_query?query="+`%7Bjob%3D%22sshd%22%7D%20%7C%3D%20%22hello%22`, nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Data   string `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	if resp.Data == "" {
		t.Fatal("expected non-empty formatted query")
	}
}

func TestFormatQueryInvalid(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/format_query?query=not-valid", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Err    string `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "invalid-query" {
		t.Fatalf("expected status invalid-query, got %q", resp.Status)
	}
	if resp.Err == "" {
		t.Fatal("expected non-empty error")
	}
}

func TestSeries(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "one"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "nginx", "MESSAGE": "two"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/series?match[]="+`{job=~".+"}`+"&start=0&end=10", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 series, got %d", len(resp.Data))
	}
}

func TestSeriesEmpty(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "one"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/series", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data != nil {
		t.Fatalf("expected nil data, got %v", resp.Data)
	}
}

func TestIndexStats(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "nginx", "MESSAGE": "world!"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/index/stats?start=0&end=10", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Streams uint64 `json:"streams"`
		Chunks  uint64 `json:"chunks"`
		Entries uint64 `json:"entries"`
		Bytes   uint64 `json:"bytes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Entries != 2 {
		t.Errorf("expected 2 entries, got %d", resp.Entries)
	}
	if resp.Streams != 2 {
		t.Errorf("expected 2 streams, got %d", resp.Streams)
	}
	if resp.Bytes != uint64(len("hello")+len("world!")) {
		t.Errorf("expected %d bytes, got %d", len("hello")+len("world!"), resp.Bytes)
	}
}

func TestIndexStatsMissingBounds(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	// Missing start/end should still work (defaults to now-1h to now).
	req := httptest.NewRequest("GET", "/loki/api/v1/index/stats", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestQueryRangeDirection(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "first"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "second"}},
		{seqnum: 3, realtime: 3_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "third"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query_range?query="+`{job="sshd"}`+"&start=0&end=10&limit=2&direction=BACKWARD", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.ResultType != "streams" {
		t.Fatalf("expected resultType streams, got %q", resp.Data.ResultType)
	}
}

func TestQueryRangeLimit(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "a"}},
		{seqnum: 2, realtime: 2_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "b"}},
		{seqnum: 3, realtime: 3_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "c"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/query_range?query="+`{job="sshd"}`+"&start=0&end=10&limit=2", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.ResultType != "streams" {
		t.Fatalf("expected resultType streams, got %q", resp.Data.ResultType)
	}

	// Parse the streams array to count entries.
	var streams []struct {
		Entries []interface{} `json:"values"`
	}
	if err := json.Unmarshal(resp.Data.Result, &streams); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range streams {
		total += len(s.Entries)
	}
	if total != 2 {
		t.Fatalf("expected 2 total entries with limit, got %d", total)
	}
}

func TestFormatQueryEmpty(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	req := httptest.NewRequest("GET", "/loki/api/v1/format_query", nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Data   string `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "success" {
		t.Fatalf("expected status success, got %q", resp.Status)
	}
}

func TestParseDirection(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "BACKWARD"},
		{"FORWARD", "FORWARD"},
		{"BACKWARD", "BACKWARD"},
		{"forward", "FORWARD"},
		{"backward", "BACKWARD"},
		{"invalid", "BACKWARD"},
	}
	for _, tt := range tests {
		d := parseDirection(tt.input)
		got := fmt.Sprintf("%v", d)
		want := fmt.Sprintf("%v", tt.want)
		if got != want {
			t.Errorf("parseDirection(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", defaultQueryLimit},
		{"50", 50},
		{"0", defaultQueryLimit},
		{"-1", defaultQueryLimit},
		{"abc", defaultQueryLimit},
	}
	for _, tt := range tests {
		got := parseLimit(tt.input)
		if got != tt.want {
			t.Errorf("parseLimit(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	zero := time.Time{}

	// Empty returns default.
	ts, err := parseTimestamp("", zero)
	if err != nil {
		t.Fatal(err)
	}
	if !ts.Equal(zero) {
		t.Errorf("expected zero time, got %v", ts)
	}

	// Unix epoch integer.
	ts, err = parseTimestamp("1000000", zero)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Unix() != 1000000 {
		t.Errorf("expected unix 1000000, got %v", ts)
	}

	// Unix epoch float.
	ts, err = parseTimestamp("1000000.5", zero)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Unix() != 1000000 {
		t.Errorf("expected unix 1000000, got %v", ts)
	}

	// RFC3339.
	ts, err = parseTimestamp("2024-01-01T00:00:00Z", zero)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Year() != 2024 {
		t.Errorf("expected year 2024, got %v", ts)
	}

	// Invalid.
	_, err = parseTimestamp("not-a-timestamp", zero)
	if err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

func TestNotImplemented(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/loki/api/v1/tail"},
		{"GET", "/loki/api/v1/patterns"},
		{"GET", "/loki/api/v1/index/volume"},
		{"GET", "/loki/api/v1/index/volume_range"},
		{"GET", "/loki/api/v1/detected_fields"},
		{"GET", "/loki/api/v1/rules"},
		{"POST", "/loki/api/v1/rules"},
		{"DELETE", "/loki/api/v1/rules/fake/group"},
	}

	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			w := httptest.NewRecorder()
			h.Handler().ServeHTTP(w, req)

			if w.Code != 501 {
				t.Errorf("%s %s: expected 501, got %d", ep.method, ep.path, w.Code)
			}
			var resp struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}
			if resp.Status != "error" {
				t.Errorf("expected status error, got %q", resp.Status)
			}
		})
	}
}

func TestReadOnly(t *testing.T) {
	h, _ := setupTestHandler(t, []testEntry{
		{seqnum: 1, realtime: 1_000_000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/loki/api/v1/push"},
		{"POST", "/otlp/v1/logs"},
		{"POST", "/loki/api/v1/delete"},
		{"GET", "/flush"},
		{"POST", "/flush"},
		{"GET", "/shutdown"},
		{"POST", "/shutdown"},
		{"GET", "/ring"},
		{"POST", "/ingester/flush"},
		{"POST", "/ingester/shutdown"},
	}

	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			w := httptest.NewRecorder()
			h.Handler().ServeHTTP(w, req)

			if w.Code != 501 {
				t.Errorf("%s %s: expected 501, got %d", ep.method, ep.path, w.Code)
			}
			var resp struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}
			if resp.Status != "error" {
				t.Errorf("expected status error, got %q", resp.Status)
			}
			if !strings.Contains(resp.Message, "read-only") {
				t.Errorf("expected read-only message, got %q", resp.Message)
			}
		})
	}
}
