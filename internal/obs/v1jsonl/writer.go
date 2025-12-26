package v1jsonl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Envelope struct {
	Type string `json:"type"`
	TsMS int64  `json:"ts_ms"`
	Data any    `json:"data"`
}

type Writer struct {
	path string

	mu sync.Mutex
}

func NewWriter(path string) *Writer {
	if strings.TrimSpace(path) == "" {
		path = filepath.FromSlash("var/contract_v1.jsonl")
	}
	return &Writer{path: path}
}

func (w *Writer) WriteEvent(typ string, data any) error {
	if w == nil {
		return nil
	}
	typ = strings.TrimSpace(typ)
	if typ == "" {
		return fmt.Errorf("event type empty")
	}

	// Redact sensitive values in a best-effort way.
	safeData, err := sanitizeToJSONSafe(data)
	if err != nil {
		// If sanitization fails, fall back to original data (still best-effort).
		safeData = data
	}

	env := Envelope{
		Type: typ,
		TsMS: time.Now().UnixMilli(),
		Data: safeData,
	}
	line, err := json.Marshal(env)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(line); err != nil {
		return err
	}
	return nil
}

var sensitiveKeyRe = regexp.MustCompile(`(?i)(secret|token|passphrase|private|api[_-]?key|access[_-]?key)`)

func sanitizeToJSONSafe(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	redactAny(&out)
	return out, nil
}

func redactAny(v *any) {
	if v == nil || *v == nil {
		return
	}
	switch t := (*v).(type) {
	case map[string]any:
		for k, vv := range t {
			if sensitiveKeyRe.MatchString(k) {
				t[k] = "[REDACTED]"
				continue
			}
			tmp := vv
			redactAny(&tmp)
			t[k] = tmp
		}
	case []any:
		for i := range t {
			tmp := t[i]
			redactAny(&tmp)
			t[i] = tmp
		}
	default:
		// If it is a map-like type that survived as map[string]interface{} via reflection.
		rv := reflect.ValueOf(*v)
		if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
			iter := rv.MapRange()
			for iter.Next() {
				k := iter.Key().String()
				val := iter.Value().Interface()
				if sensitiveKeyRe.MatchString(k) {
					rv.SetMapIndex(iter.Key(), reflect.ValueOf("[REDACTED]"))
					continue
				}
				tmp := val
				redactAny(&tmp)
				rv.SetMapIndex(iter.Key(), reflect.ValueOf(tmp))
			}
		}
	}
}
