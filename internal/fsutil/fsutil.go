// Package fsutil holds queqiao's file helpers: where its config lives,
// atomic writes, and key-path edits of JSON files that keep the rest of
// the file (its other keys and their order) as it was.
package fsutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ConfigDir is where queqiao keeps its own files: $QUEQIAO_CONFIG_DIR when
// set, else $XDG_CONFIG_HOME/queqiao, else ~/.config/queqiao.
func ConfigDir() string {
	if d := os.Getenv("QUEQIAO_CONFIG_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "queqiao")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "queqiao")
}

// WriteAtomic writes b to path through a temporary file in the same
// directory and a rename, keeping an existing file's permissions.
func WriteAtomic(path string, b []byte) error {
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// KV is one edit: a dot-separated key path and the value to put there.
type KV struct {
	Path  string
	Value any
}

// SetJSON sets each key path in the JSON object file at path, creating
// objects along the way and the file when it is missing. A file that
// exists but is not a JSON object is an error and is left untouched.
func SetJSON(path string, kv ...KV) error {
	root, err := load(path)
	if err != nil {
		return err
	}
	for _, e := range kv {
		v, err := encode(e.Value)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Path, err)
		}
		if err := root.set(strings.Split(e.Path, "."), v); err != nil {
			return fmt.Errorf("%s: %s: %w", path, e.Path, err)
		}
	}
	return save(path, root)
}

// GetJSON returns the raw value at a dotted key path, and whether it is
// there. A missing file is (nil, false, nil); an unparseable one an error.
func GetJSON(path, dotted string) (json.RawMessage, bool, error) {
	root, err := load(path)
	if err != nil {
		return nil, false, err
	}
	n := root
	keys := strings.Split(dotted, ".")
	for i, k := range keys {
		v, ok := n.get(k)
		if !ok {
			return nil, false, nil
		}
		if i == len(keys)-1 {
			return v.bytes(), true, nil
		}
		if v.obj == nil {
			return nil, false, nil
		}
		n = v.obj
	}
	return nil, false, nil
}

// DelJSON removes the key at a dotted path; a missing key is no error.
func DelJSON(path, dotted string) error {
	root, err := load(path)
	if err != nil {
		return err
	}
	keys := strings.Split(dotted, ".")
	n := root
	for _, k := range keys[:len(keys)-1] {
		v, ok := n.get(k)
		if !ok || v.obj == nil {
			return nil
		}
		n = v.obj
	}
	n.del(keys[len(keys)-1])
	return save(path, root)
}

// object is a JSON object that remembers its key order; a value is either
// an object or the raw bytes of anything else.
type object struct {
	keys []string
	vals map[string]*value
}

type value struct {
	obj *object
	raw json.RawMessage
}

func (o *object) get(k string) (*value, bool) { v, ok := o.vals[k]; return v, ok }

func (o *object) put(k string, v *value) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) set(keys []string, v *value) error {
	if len(keys) == 1 {
		o.put(keys[0], v)
		return nil
	}
	cur, ok := o.get(keys[0])
	if !ok {
		cur = &value{obj: newObject()}
		o.put(keys[0], cur)
	}
	if cur.obj == nil {
		return errors.New("not an object at " + keys[0])
	}
	return cur.obj.set(keys[1:], v)
}

func newObject() *object { return &object{vals: map[string]*value{}} }

func (v *value) bytes() []byte {
	if v.obj == nil {
		return v.raw
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range v.obj.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(v.obj.vals[k].bytes())
	}
	b.WriteByte('}')
	return b.Bytes()
}

func encode(x any) (*value, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(x); err != nil {
		return nil, err
	}
	return parseValue(bytes.TrimSpace(b.Bytes()))
}

func parseValue(b []byte) (*value, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		if !json.Valid(b) {
			return nil, errors.New("invalid JSON")
		}
		return &value{raw: append(json.RawMessage(nil), b...)}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	o := newObject()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.Token() // {
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		v, err := parseValue(raw)
		if err != nil {
			return nil, err
		}
		o.put(k, v)
	}
	return &value{obj: o}, nil
}

func load(path string) (*object, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newObject(), nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return newObject(), nil
	}
	v, err := parseValue(b)
	if err != nil {
		return nil, fmt.Errorf("%s: not readable JSON: %w", path, err)
	}
	if v.obj == nil {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	return v.obj, nil
}

func save(path string, root *object) error {
	var out bytes.Buffer
	if err := json.Indent(&out, (&value{obj: root}).bytes(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	return WriteAtomic(path, out.Bytes())
}
