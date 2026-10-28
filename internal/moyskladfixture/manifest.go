package moyskladfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func AccountHash(account string) (string, error) {
	if !uuidPattern.MatchString(account) {
		return "", errors.New("configured account ID must be a UUID")
	}
	h := sha256.Sum256([]byte(account))
	return hex.EncodeToString(h[:]), nil
}

// Lock serializes all local actions on one fixture namespace, including remote
// calls, so that two administrators cannot concurrently submit the same POST.
type Lock struct {
	directory, namespace string
	lock                 *os.File
}

func OpenLock(directory, namespace string) (*Lock, error) {
	if !namespacePattern.MatchString(namespace) {
		return nil, errors.New("invalid fixture namespace")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create fixture directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("fixture directory must be a private directory")
	}
	name := filepath.Join(directory, namespace+".lock")
	fd, err := syscall.Open(name, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open fixture lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), name)
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("fixture namespace is already in use")
	}
	return &Lock{directory: directory, namespace: namespace, lock: f}, nil
}

func (l *Lock) Close() error {
	if err := syscall.Flock(int(l.lock.Fd()), syscall.LOCK_UN); err != nil {
		l.lock.Close()
		return err
	}
	return l.lock.Close()
}

func (l *Lock) path() string { return filepath.Join(l.directory, l.namespace+".json") }

func validateManifest(m Manifest, namespace string) error {
	if m.Version != 1 || m.Namespace != namespace || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(m.AccountHash) {
		return errors.New("invalid fixture manifest header")
	}
	expected, err := Plan(namespace)
	if err != nil {
		return err
	}
	if len(m.Operations) == len(expected)+2 {
		extension, extensionErr := VariantExtension(namespace)
		if extensionErr != nil || !m.MultipleOrderProductOnly {
			return errors.New("invalid fixture continuation")
		}
		expected = append(expected, extension...)
	} else if m.MultipleOrderProductOnly {
		return errors.New("unexpected fixture continuation state")
	}
	if len(m.Operations) != len(expected) {
		return errors.New("fixture manifest plan changed")
	}
	allowedCounts := map[string]bool{"store": true, "product": true, "variant": true, "customerorder": true, "enter": true, "supply": true, "demand": true, "inventory": true, "organization": true}
	for key, value := range m.InitialCounts {
		if !allowedCounts[key] || value < 0 {
			return errors.New("invalid fixture preflight counts")
		}
	}
	allowedStock := map[string]bool{"product-a/warehouse-a": true, "product-a/warehouse-b": true, "product-b/warehouse-a": true, "product-c/warehouse-b": true, "variant-one/warehouse-a": true, "variant-two/warehouse-a": true}
	for key, v := range m.StockChecks {
		if !allowedStock[key] {
			return errors.New("invalid fixture stock key")
		}
		switch v.Status {
		case "MISSING", "MISSING_VALUE", "STOCK_MATCH", "STOCK_MISMATCH":
		default:
			return errors.New("invalid fixture stock status")
		}
		for _, number := range []*string{v.Stock, v.Reserve, v.Incoming} {
			if number != nil {
				if len(*number) > 100 {
					return errors.New("invalid fixture quantity")
				}
				if _, ok := parseAmount(*number); !ok {
					return errors.New("invalid fixture quantity")
				}
			}
		}
	}
	for i, o := range m.Operations {
		if o.Key != expected[i].Key || o.Kind != expected[i].Kind || o.Identity != expected[i].Identity {
			return errors.New("fixture manifest operation does not match the built-in plan")
		}
		switch o.State {
		case Planned, Created, FailedBeforeSend, Unknown, Skipped:
		default:
			return errors.New("invalid fixture state")
		}
		if o.RemoteID != "" && !uuidPattern.MatchString(o.RemoteID) {
			return errors.New("invalid remote identifier")
		}
		if o.State == Created && o.RemoteID == "" {
			return errors.New("verified fixture lacks an identifier")
		}
		if o.State != Created && o.State != Unknown && o.RemoteID != "" {
			return errors.New("unsent fixture has a remote identifier")
		}
		if !m.NamespaceChecked && (o.State == Unknown || o.State == Created || o.State == Skipped) {
			return errors.New("fixture state precedes namespace verification")
		}
	}
	return nil
}

func (l *Lock) Load() (Manifest, error) {
	fd, err := syscall.Open(l.path(), syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return Manifest{}, fmt.Errorf("open fixture manifest: %w", err)
	}
	f := os.NewFile(uintptr(fd), l.path())
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return Manifest{}, errors.New("fixture manifest must be a private regular file")
	}
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	var m Manifest
	if err = dec.Decode(&m); err != nil {
		return m, errors.New("invalid fixture manifest")
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return m, errors.New("invalid trailing manifest data")
	}
	return m, validateManifest(m, l.namespace)
}

// Create uses exclusive creation so a repeated dry-run cannot replace history.
func (l *Lock) Create(account string) (Manifest, error) {
	h, err := AccountHash(account)
	if err != nil {
		return Manifest{}, err
	}
	ops, err := Plan(l.namespace)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: 1, Namespace: l.namespace, AccountHash: h, Operations: ops}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	fd, err := syscall.Open(l.path(), syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return m, fmt.Errorf("create fixture manifest: %w", err)
	}
	f := os.NewFile(uintptr(fd), l.path())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return m, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return m, err
	}
	if err = f.Close(); err != nil {
		return m, err
	}
	return m, syncDirectory(l.directory)
}

// Save uses a same-directory atomic replacement and fsyncs both file and directory.
func (l *Lock) Save(m Manifest) error {
	if err := validateManifest(m, l.namespace); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(l.directory, ".manifest-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, l.path()); err != nil {
		return err
	}
	return syncDirectory(l.directory)
}

func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
