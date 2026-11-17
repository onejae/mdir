package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

type archiveMember struct {
	Entry
	regular  bool
	explicit bool
}

// Archive holds an index, not decompressed file contents. Paths are relative,
// slash-separated archive names, including synthesized parent directories.
type Archive struct {
	Path    string
	format  string
	entries map[string]archiveMember
}

func IsArchive(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

func OpenArchive(name string) (*Archive, error) {
	return openArchive(context.Background(), name)
}

func openArchive(ctx context.Context, name string) (*Archive, error) {
	f, err := openArchiveSource(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return indexArchive(ctx, name, f)
}

func openArchiveSource(name string) (*os.File, error) {
	// Follow an explicitly selected archive symlink, but never block on a FIFO
	// or accept a device substituted for the regular archive before open.
	f, err := os.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("archive is not a regular file: %s", name)
	}
	return f, nil
}

func indexArchive(ctx context.Context, name string, f *os.File) (*Archive, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format, err := archiveFormat(name, f)
	if err != nil {
		return nil, err
	}
	a := &Archive{Path: name, format: format, entries: map[string]archiveMember{}}
	err = a.visitFile(ctx, f, func(member archiveMember, _ io.Reader) error {
		if member.Path == "" {
			return nil
		}
		for parent := path.Dir(member.Path); parent != "."; parent = path.Dir(parent) {
			if old, ok := a.entries[parent]; ok {
				if !old.IsDir || old.IsLink {
					return fmt.Errorf("archive path collision: %s", parent)
				}
				continue
			}
			a.entries[parent] = archiveMember{Entry: Entry{Name: path.Base(parent), Path: parent, IsDir: true, Mode: os.ModeDir | 0755}}
		}
		if old, ok := a.entries[member.Path]; ok {
			if old.explicit || !old.IsDir || !member.IsDir || member.IsLink {
				return fmt.Errorf("duplicate or conflicting archive path: %s", member.Path)
			}
		}
		a.entries[member.Path] = member
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func archiveFormat(name string, f *os.File) (string, error) {
	var signature [4]byte
	n, err := f.ReadAt(signature[:], 0)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	if n >= 2 && signature[0] == 0x1f && signature[1] == 0x8b {
		return "tgz", nil
	}
	if n == 4 && signature[0] == 'P' && signature[1] == 'K' && (signature[2] == 3 || signature[2] == 5 || signature[2] == 7) {
		return "zip", nil
	}
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		return "zip", nil
	}
	return "tar", nil
}

func cleanArchiveName(name string, directory bool) (string, error) {
	if strings.ContainsRune(name, '\x00') || strings.Contains(name, `\`) || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return "", fmt.Errorf("unsafe archive path: %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe archive path: %q", name)
		}
	}
	name = path.Clean(name)
	if name == "." {
		if directory {
			return "", nil
		}
		return "", fmt.Errorf("invalid empty archive filename")
	}
	return name, nil
}

func (a *Archive) visit(ctx context.Context, visit func(archiveMember, io.Reader) error) error {
	f, err := openArchiveSource(a.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	return a.visitFile(ctx, f, visit)
}

func (a *Archive) visitFile(ctx context.Context, f *os.File, visit func(archiveMember, io.Reader) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	archiveInfo, err := f.Stat()
	if err != nil {
		return err
	}
	if a.format == "zip" {
		reader, err := zip.NewReader(f, archiveInfo.Size())
		if err != nil {
			return err
		}
		for _, file := range reader.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			info := file.FileInfo()
			name, err := cleanArchiveName(file.Name, info.IsDir())
			if err != nil {
				return err
			}
			if info.Size() < 0 {
				return fmt.Errorf("archive entry is too large: %s", name)
			}
			member := archiveMember{Entry: entryFor(path.Base(name), name, info), regular: info.Mode().IsRegular(), explicit: true}
			contents, err := file.Open()
			if err != nil {
				return err
			}
			err = visit(member, contextReader{ctx, contents})
			closeErr := contents.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	var reader io.Reader = contextReader{ctx, io.NewSectionReader(f, 0, archiveInfo.Size())}
	var compressed *gzip.Reader
	if a.format == "tgz" {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader = gz
		compressed = gz
	}
	tr := tar.NewReader(contextReader{ctx, reader})
	for {
		header, err := tr.Next()
		if err == io.EOF {
			if compressed != nil {
				// TAR ends before gzip's footer. Reading through gzip EOF is
				// required to verify its checksum and declared content length.
				if err := copyData(ctx, io.Discard, compressed); err != nil {
					return err
				}
			}
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		info := header.FileInfo()
		name, err := cleanArchiveName(header.Name, info.IsDir())
		if err != nil {
			return err
		}
		member := archiveMember{Entry: entryFor(path.Base(name), name, info), regular: header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA, explicit: true}
		if header.Typeflag == tar.TypeLink {
			member.IsLink = true
		}
		if err := visit(member, tr); err != nil {
			return err
		}
	}
}

func (a *Archive) List(prefix string) ([]Entry, error) {
	prefix, err := cleanArchiveName(prefix, true)
	if err != nil {
		return nil, err
	}
	if prefix != "" {
		member, ok := a.entries[prefix]
		if !ok || !member.IsDir {
			return nil, fmt.Errorf("archive directory not found: %s", prefix)
		}
	}
	entries := []Entry{}
	for name, member := range a.entries {
		parent := path.Dir(name)
		if parent == "." {
			parent = ""
		}
		if parent == prefix {
			entries = append(entries, member.Entry)
		}
	}
	sortEntries(entries, "name", false)
	return entries, nil
}

var errArchiveFound = errors.New("archive entry found")

// Read returns at most limit bytes; callers may use a bounded prefix for preview.
func (a *Archive) Read(name string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("negative archive read limit")
	}
	name, err := cleanArchiveName(name, false)
	if err != nil {
		return nil, err
	}
	member, ok := a.entries[name]
	if !ok {
		return nil, fmt.Errorf("archive file %s: %w", name, fs.ErrNotExist)
	}
	if !member.regular || member.IsLink {
		return nil, fmt.Errorf("not a regular archive file: %s", name)
	}
	var data []byte
	err = a.visit(context.Background(), func(current archiveMember, reader io.Reader) error {
		if current.Path != name {
			return nil
		}
		if !current.regular || current.IsLink {
			return fmt.Errorf("archive changed: %s", name)
		}
		data, err = io.ReadAll(io.LimitReader(reader, limit))
		if err != nil {
			return err
		}
		return errArchiveFound
	})
	if errors.Is(err, errArchiveFound) {
		return data, nil
	}
	if err == nil {
		err = fmt.Errorf("archive file %s: %w", name, fs.ErrNotExist)
	}
	return nil, err
}

func (a *Archive) selected(names []string) (map[string]archiveMember, error) {
	selected := map[string]archiveMember{}
	if len(names) == 0 {
		for name, member := range a.entries {
			selected[name] = member
		}
		return selected, nil
	}
	for _, name := range names {
		name, err := cleanArchiveName(name, true)
		if err != nil {
			return nil, err
		}
		member, ok := a.entries[name]
		if !ok {
			return nil, fmt.Errorf("archive entry %s: %w", name, fs.ErrNotExist)
		}
		selected[name] = member
		if member.IsDir {
			for child, childMember := range a.entries {
				if strings.HasPrefix(child, name+"/") {
					selected[child] = childMember
				}
			}
		}
	}
	return selected, nil
}

// Extract writes only regular files and directories. Existing files and links
// are never replaced; unsafe archive names are rejected while indexing.
func (a *Archive) Extract(ctx context.Context, names []string, dest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep the same open file for indexing and extraction: replacing its path
	// must not silently switch the archive after its members were validated.
	source, err := openArchiveSource(a.Path)
	if err != nil {
		return err
	}
	defer source.Close()
	snapshot, err := source.Stat()
	if err != nil {
		return err
	}
	current, err := indexArchive(ctx, a.Path, source)
	if err != nil {
		return err
	}
	selected, err := current.selected(names)
	if err != nil {
		return err
	}
	for _, member := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		if member.IsLink || (!member.IsDir && !member.regular) {
			return fmt.Errorf("refusing archive link or special file: %s", member.Path)
		}
	}
	root, err := openExtractionRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	created := map[string]bool{}
	processed := map[string]bool{}
	// Create parents on demand, recording only directories this extraction owns.
	for name, member := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		if member.IsDir {
			dir, err := extractDirectory(root, name, created)
			if err != nil {
				return err
			}
			dir.Close()
		}
	}
	err = current.visitFile(ctx, source, func(member archiveMember, reader io.Reader) error {
		if _, ok := selected[member.Path]; !ok || member.IsDir {
			return nil
		}
		if !member.regular || member.IsLink {
			return fmt.Errorf("refusing archive link or special file: %s", member.Path)
		}
		parent, err := extractDirectory(root, path.Dir(member.Path), created)
		if err != nil {
			return err
		}
		defer parent.Close()
		if err := writeExclusive(ctx, parent, path.Base(member.Path), reader, member.Mode.Perm(), member.ModTime); err != nil {
			return err
		}
		processed[member.Path] = true
		return nil
	})
	if err != nil {
		return err
	}
	for name, member := range selected {
		if !member.IsDir && !processed[name] {
			return fmt.Errorf("archive changed: selected file was not extracted: %s", name)
		}
	}
	currentInfo, err := source.Stat()
	if err != nil {
		return err
	}
	if !sameFileVersion(snapshot, currentInfo) {
		return fmt.Errorf("archive changed during extraction: %s", a.Path)
	}
	// Apply directory metadata after children have been written, deepest first.
	dirs := make([]string, 0, len(created))
	for name := range created {
		dirs = append(dirs, name)
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.Count(dirs[i], "/") > strings.Count(dirs[j], "/") })
	for _, name := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		member := current.entries[name]
		dir, err := extractDirectory(root, name, nil)
		if err != nil {
			return err
		}
		f, err := dir.Open(".")
		dir.Close()
		if err != nil {
			return err
		}
		err = setMetadata(f, member.Mode.Perm(), member.ModTime)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// A user-supplied destination may be new. Anchor its existing parent first,
// then exclusively create/open its last component without following a link.
func openExtractionRoot(dest string) (*os.Root, error) {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return nil, err
	}
	if abs == string(filepath.Separator) {
		return os.OpenRoot(abs)
	}
	parentPath := filepath.Dir(abs)
	parent, err := os.OpenRoot(parentPath)
	if errors.Is(err, fs.ErrNotExist) {
		parent, err = openExtractionRoot(parentPath)
	}
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := filepath.Base(abs)
	if err := parent.Mkdir(name, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	return openChildRoot(parent, name)
}

func extractDirectory(root *os.Root, name string, created map[string]bool) (*os.Root, error) {
	dir, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if name == "" || name == "." {
		return dir, nil
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		if created != nil {
			err := dir.Mkdir(part, 0700)
			if err == nil {
				created[strings.Join(parts[:i+1], "/")] = true
			} else if !errors.Is(err, fs.ErrExist) {
				dir.Close()
				return nil, err
			}
		}
		next, err := openChildRoot(dir, part)
		dir.Close()
		if err != nil {
			return nil, fmt.Errorf("unsafe or conflicting extraction directory %s: %w", name, err)
		}
		dir = next
	}
	return dir, nil
}

// CreateArchive creates a new ZIP, TAR or gzip-compressed TAR, storing symlinks
// as links and never following them. Destination must be outside source trees.
func CreateArchive(ctx context.Context, format string, sources []string, dest string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	format = strings.ToLower(format)
	if format == "tar.gz" {
		format = "tgz"
	}
	if format != "zip" && format != "tar" && format != "tgz" {
		return fmt.Errorf("unsupported archive format: %s", format)
	}
	if len(sources) == 0 {
		return fmt.Errorf("no archive sources")
	}
	infos := make([]fs.FileInfo, len(sources))
	seen := map[string]bool{}
	for i, source := range sources {
		infos[i], err = os.Lstat(source)
		if err != nil {
			return err
		}
		if err := checkDestination(source, dest, infos[i]); err != nil {
			return err
		}
		name := filepath.Base(filepath.Clean(source))
		if _, err := cleanArchiveName(name, false); err != nil || seen[name] {
			return fmt.Errorf("invalid or duplicate archive source name: %s", name)
		}
		seen[name] = true
	}
	parent, err := os.OpenRoot(filepath.Dir(dest))
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(dest)
	for i, source := range sources {
		if err := checkRootDestination(source, name, infos[i], parent); err != nil {
			return err
		}
	}
	f, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	created, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	defer func() {
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			if info, statErr := parent.Lstat(name); statErr == nil && os.SameFile(created, info) {
				_ = parent.Remove(name)
			}
		}
	}()
	var zipWriter *zip.Writer
	var tarWriter *tar.Writer
	var gzipWriter *gzip.Writer
	if format == "zip" {
		zipWriter = zip.NewWriter(f)
	} else {
		var writer io.Writer = f
		if format == "tgz" {
			gzipWriter = gzip.NewWriter(f)
			writer = gzipWriter
		}
		tarWriter = tar.NewWriter(writer)
	}
	for i, source := range sources {
		if err = addArchiveEntry(ctx, sourceEntry{name: filepath.Clean(source)}, filepath.Base(filepath.Clean(source)), infos[i], zipWriter, tarWriter); err != nil {
			break
		}
	}
	if zipWriter != nil {
		err = errors.Join(err, zipWriter.Close())
	}
	if tarWriter != nil {
		err = errors.Join(err, tarWriter.Close())
	}
	if gzipWriter != nil {
		err = errors.Join(err, gzipWriter.Close())
	}
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func addArchiveEntry(ctx context.Context, source sourceEntry, name string, info fs.FileInfo, zw *zip.Writer, tw *tar.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := cleanArchiveName(name, info.IsDir()); err != nil {
		return err
	}
	link := ""
	if info.Mode()&os.ModeSymlink != 0 {
		var err error
		link, err = source.readlink()
		if err != nil {
			return err
		}
	} else if !info.IsDir() && !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported archive source type: %s", source.name)
	}
	var writer io.Writer
	if zw != nil {
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		if info.IsDir() {
			header.Name += "/"
		} else if link == "" {
			header.Method = zip.Deflate
		}
		writer, err = zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if link != "" {
			_, err := io.WriteString(writer, link)
			return err
		}
	} else {
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = name
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		writer = tw
		if link != "" {
			return nil
		}
	}
	f, err := source.open(info.IsDir())
	if err != nil {
		return err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return err
	}
	if !sameFileVersion(info, actual) {
		return fmt.Errorf("source changed while creating archive: %s", source.name)
	}
	if !info.IsDir() {
		if err := copyData(ctx, writer, f); err != nil {
			return err
		}
		current, err := f.Stat()
		if err != nil {
			return err
		}
		if !sameFileVersion(actual, current) {
			return fmt.Errorf("source changed while creating archive: %s", source.name)
		}
		return nil
	}
	children, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	root, err := rootFromFile(f)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, child := range children {
		childSource := sourceEntry{parent: root, name: child.Name()}
		childInfo, err := childSource.lstat()
		if err != nil {
			return err
		}
		if err := addArchiveEntry(ctx, childSource, path.Join(name, child.Name()), childInfo, zw, tw); err != nil {
			return err
		}
	}
	return nil
}
