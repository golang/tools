// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tar implements access to tar archives.
//
// Tape archives (tar) are a file format for storing a sequence of files that
// can be read and written in a streaming manner.
// This package aims to cover most variations of the format,
// including those produced by GNU and BSD tar tools.
package tar

import (
	"errors"
	"io"
	"os"
	"time"
)

// BUG: Use of the Uid and Gid fields in Header could overflow on 32-bit
// architectures. If a large value is encountered when decoding, the result
// stored in Header will be the truncated version.

var (
	ErrHeader          = errors.New("tar: invalid tar header")
	ErrWriteTooLong    = errors.New("tar: write too long")
	ErrFieldTooLong    = errors.New("tar: header field too long")
	ErrWriteAfterClose = errors.New("tar: write after close")
)

// Type flags for Header.Typeflag.
const (
	// Type '0' indicates a regular file.
	TypeReg  = '0'
	TypeRegA = '\x00' // For legacy support; use TypeReg instead

	// Type '1' to '6' are header-only flags and may not have a data body.
	TypeLink    = '1' // Hard link
	TypeSymlink = '2' // Symbolic link
	TypeChar    = '3' // Character device node
	TypeBlock   = '4' // Block device node
	TypeDir     = '5' // Directory
	TypeFifo    = '6' // FIFO node

	// Type '7' is reserved.
	TypeCont = '7'

	// Type 'x' is used by the PAX format to store key-value records that
	// are only relevant to the next file.
	// This package transparently handles these types.
	TypeXHeader = 'x'

	// Type 'g' is used by the PAX format to store key-value records that
	// are relevant to all subsequent files.
	// This package only supports parsing and composing such headers,
	// but does not currently support persisting the global state across files.
	TypeXGlobalHeader = 'g'

	// Type 'S' indicates a sparse file in the GNU format.
	// Header.SparseHoles should be populated when using this type.
	TypeGNUSparse = 'S'

	// Types 'L' and 'K' are used by the GNU format for a meta file
	// used to store the path or link name for the next file.
	// This package transparently handles these types.
	TypeGNULongName = 'L'
	TypeGNULongLink = 'K'
)

// A Header represents a single header in a tar archive.
// Some fields may not be populated.
//
// For forward compatibility, users that retrieve a Header from Reader.Next,
// mutate it in some ways, and then pass it back to Writer.WriteHeader
// should do so by creating a new Header and copying the fields
// that they are interested in preserving.
type Header struct {
	Typeflag byte // Type of header entry (should be TypeReg for most files)

	Name     string // Path name of entry
	Linkname string // Target name of link (valid for TypeLink or TypeSymlink)

	Size  int64  // Logical file size in bytes
	Mode  int64  // Permission and mode bits
	Uid   int    // User ID of owner
	Gid   int    // Group ID of owner
	Uname string // User name of owner
	Gname string // Group name of owner

	// If the Format is unspecified, then Writer.WriteHeader rounds ModTime
	// to the nearest second and ignores the AccessTime and ChangeTime fields.
	//
	// To use AccessTime or ChangeTime, specify the Format as PAX or GNU.
	// To use sub-second resolution, specify the Format as PAX.
	ModTime    time.Time // Modification time
	AccessTime time.Time // Access time (requires either PAX or GNU support)
	ChangeTime time.Time // Change time (requires either PAX or GNU support)

	Devmajor int64 // Major device number (valid for TypeChar or TypeBlock)
	Devminor int64 // Minor device number (valid for TypeChar or TypeBlock)

	// SparseHoles represents a sequence of holes in a sparse file.
	//
	// A file is sparse if len(SparseHoles) > 0 or Typeflag is TypeGNUSparse.
	// If TypeGNUSparse is set, then the format is GNU, otherwise
	// the format is PAX (by using GNU-specific PAX records).
	//
	// A sparse file consists of fragments of data, intermixed with holes
	// (described by this field). A hole is semantically a block of NUL-bytes,
	// but does not actually exist within the tar file.
	// The holes must be sorted in ascending order,
	// not overlap with each other, and not extend past the specified Size.
	SparseHoles []SparseEntry

	// Xattrs stores extended attributes as PAX records under the
	// "SCHILY.xattr." namespace.
	//
	// The following are semantically equivalent:
	//  h.Xattrs[key] = value
	//  h.PAXRecords["SCHILY.xattr."+key] = value
	//
	// When Writer.WriteHeader is called, the contents of Xattrs will take
	// precedence over those in PAXRecords.
	//
	// Deprecated: Use PAXRecords instead.
	Xattrs map[string]string

	// PAXRecords is a map of PAX extended header records.
	//
	// User-defined records should have keys of the following form:
	//	VENDOR.keyword
	// Where VENDOR is some namespace in all uppercase, and keyword may
	// not contain the '=' character (e.g., "GOLANG.pkg.version").
	// The key and value should be non-empty UTF-8 strings.
	//
	// When Writer.WriteHeader is called, PAX records derived from the
	// the other fields in Header take precedence over PAXRecords.
	PAXRecords map[string]string

	// Format specifies the format of the tar header.
	//
	// This is set by Reader.Next as a best-effort guess at the format.
	// Since the Reader liberally reads some non-compliant files,
	// it is possible for this to be FormatUnknown.
	//
	// If the format is unspecified when Writer.WriteHeader is called,
	// then it uses the first format (in the order of USTAR, PAX, GNU)
	// capable of encoding this Header (see tar.Format).
	Format Format
}

// SparseEntry represents a Length sized fragment at Offset in the file.
type SparseEntry struct{ Offset, Length int64 }

// DetectSparseHoles searches for holes within f to populate SparseHoles
// on supported operating systems and filesystems.
// The file offset is cleared to zero.
//
// When packing a sparse file, DetectSparseHoles should be called prior to
// serializing the header to the archive with Writer.WriteHeader.
func (h *Header) DetectSparseHoles(f *os.File) (err error) {
	return nil
}

// PunchSparseHoles destroys the contents of f, and prepares a sparse file
// (on supported operating systems and filesystems)
// with holes punched according to SparseHoles.
// The file offset is cleared to zero.
//
// When extracting a sparse file, PunchSparseHoles should be called prior to
// populating the content of a file with Reader.WriteTo.
func (h *Header) PunchSparseHoles(f *os.File) (err error) {
	return nil
}

// FileInfo returns an os.FileInfo for the Header.
func (h *Header) FileInfo() os.FileInfo {
	return nil
}

// FileInfoHeader creates a partially-populated Header from fi.
// If fi describes a symlink, FileInfoHeader records link as the link target.
// If fi describes a directory, a slash is appended to the name.
//
// Since os.FileInfo's Name method only returns the base name of
// the file it describes, it may be necessary to modify Header.Name
// to provide the full path name of the file.
//
// This function does not populate Header.SparseHoles;
// for sparse file support, additionally call Header.DetectSparseHoles.
func FileInfoHeader(fi os.FileInfo, link string) (*Header, error) {
	return nil, nil
}

// Format represents the tar archive format.
//
// The original tar format was introduced in Unix V7.
// Since then, there have been multiple competing formats attempting to
// standardize or extend the V7 format to overcome its limitations.
// The most common formats are the USTAR, PAX, and GNU formats,
// each with their own advantages and limitations.
//
// The following table captures the capabilities of each format:
//
//	                  |  USTAR |       PAX |       GNU
//	------------------+--------+-----------+----------
//	Name              |   256B | unlimited | unlimited
//	Linkname          |   100B | unlimited | unlimited
//	Size              | uint33 | unlimited |    uint89
//	Mode              | uint21 |    uint21 |    uint57
//	Uid/Gid           | uint21 | unlimited |    uint57
//	Uname/Gname       |    32B | unlimited |       32B
//	ModTime           | uint33 | unlimited |     int89
//	AccessTime        |    n/a | unlimited |     int89
//	ChangeTime        |    n/a | unlimited |     int89
//	Devmajor/Devminor | uint21 |    uint21 |    uint57
//	------------------+--------+-----------+----------
//	string encoding   |  ASCII |     UTF-8 |    binary
//	sub-second times  |     no |       yes |        no
//	sparse files      |     no |       yes |       yes
//
// The table's upper portion shows the Header fields, where each format reports
// the maximum number of bytes allowed for each string field and
// the integer type used to store each numeric field
// (where timestamps are stored as the number of seconds since the Unix epoch).
//
// The table's lower portion shows specialized features of each format,
// such as supported string encodings, support for sub-second timestamps,
// or support for sparse files.
type Format int

// Constants to identify various tar formats.
const (
	// Deliberately hide the meaning of constants from public API.
	_ Format = (1 << iota) / 4 // Sequence of 0, 0, 1, 2, 4, 8, etc...

	// FormatUnknown indicates that the format is unknown.
	FormatUnknown

	// The format of the original Unix V7 tar tool prior to standardization.
	formatV7

	// FormatUSTAR represents the USTAR header format defined in POSIX.1-1988.
	//
	// While this format is compatible with most tar readers,
	// the format has several limitations making it unsuitable for some usages.
	// Most notably, it cannot support sparse files, files larger than 8GiB,
	// filenames larger than 256 characters, and non-ASCII filenames.
	//
	// Reference:
	//	http://pubs.opengroup.org/onlinepubs/9699919799/utilities/pax.html#tag_20_92_13_06
	FormatUSTAR

	// FormatPAX represents the PAX header format defined in POSIX.1-2001.
	//
	// PAX extends USTAR by writing a special file with Typeflag TypeXHeader
	// preceding the original header. This file contains a set of key-value
	// records, which are used to overcome USTAR's shortcomings, in addition to
	// providing the ability to have sub-second resolution for timestamps.
	//
	// Some newer formats add their own extensions to PAX by defining their
	// own keys and assigning certain semantic meaning to the associated values.
	// For example, sparse file support in PAX is implemented using keys
	// defined by the GNU manual (e.g., "GNU.sparse.map").
	//
	// Reference:
	//	http://pubs.opengroup.org/onlinepubs/009695399/utilities/pax.html
	FormatPAX

	// FormatGNU represents the GNU header format.
	//
	// The GNU header format is older than the USTAR and PAX standards and
	// is not compatible with them. The GNU format supports
	// arbitrary file sizes, filenames of arbitrary encoding and length,
	// sparse files, and other features.
	//
	// It is recommended that PAX be chosen over GNU unless the target
	// application can only parse GNU formatted archives.
	//
	// Reference:
	//	http://www.gnu.org/software/tar/manual/html_node/Standard.html
	FormatGNU

	// Schily's tar format, which is incompatible with USTAR.
	// This does not cover STAR extensions to the PAX format; these fall under
	// the PAX format.
	formatSTAR

	formatMax
)

func (f Format) String() string {
	return ""
}

// Reader provides sequential access to the contents of a tar archive.
// Reader.Next advances to the next file in the archive (including the first),
// and then Reader can be treated as an io.Reader to access the file's data.
type Reader struct {
	unexported struct{}
}

// NewReader creates a new Reader reading from r.
func NewReader(r io.Reader) *Reader {
	return nil
}

// Next advances to the next entry in the tar archive.
// The Header.Size determines how many bytes can be read for the next file.
// Any remaining data in the current file is automatically discarded.
//
// io.EOF is returned at the end of the input.
func (tr *Reader) Next() (*Header, error) {
	return nil, nil
}

// Read reads from the current file in the tar archive.
// It returns (0, io.EOF) when it reaches the end of that file,
// until Next is called to advance to the next file.
//
// If the current file is sparse, then the regions marked as a hole
// are read back as NUL-bytes.
//
// Calling Read on special types like TypeLink, TypeSymlink, TypeChar,
// TypeBlock, TypeDir, and TypeFifo returns (0, io.EOF) regardless of what
// the Header.Size claims.
func (tr *Reader) Read(b []byte) (int, error) {
	return 0, nil
}

// WriteTo writes the content of the current file to w.
// The bytes written matches the number of remaining bytes in the current file.
//
// If the current file is sparse and w is an io.WriteSeeker,
// then WriteTo uses Seek to skip past holes defined in Header.SparseHoles,
// assuming that skipped regions are filled with NULs.
// This always writes the last byte to ensure w is the right size.
func (tr *Reader) WriteTo(w io.Writer) (int64, error) {
	return 0, nil
}

// Writer provides sequential writing of a tar archive.
// Writer.WriteHeader begins a new file with the provided Header,
// and then Writer can be treated as an io.Writer to supply that file's data.
type Writer struct {
	unexported struct{}
}

// NewWriter creates a new Writer writing to w.
func NewWriter(w io.Writer) *Writer {
	return nil
}

// Flush finishes writing the current file's block padding.
// The current file must be fully written before Flush can be called.
//
// Deprecated: This is unnecessary as the next call to WriteHeader or Close
// will implicitly flush out the file's padding.
func (tw *Writer) Flush() error {
	return nil
}

// WriteHeader writes hdr and prepares to accept the file's contents.
// The Header.Size determines how many bytes can be written for the next file.
// If the current file is not fully written, then this returns an error.
// This implicitly flushes any padding necessary before writing the header.
func (tw *Writer) WriteHeader(hdr *Header) error {
	return nil
}

// Write writes to the current file in the tar archive.
// Write returns the error ErrWriteTooLong if more than
// Header.Size bytes are written after WriteHeader.
//
// If the current file is sparse, then the regions marked as a hole
// must be written as NUL-bytes.
//
// Calling Write on special types like TypeLink, TypeSymlink, TypeChar,
// TypeBlock, TypeDir, and TypeFifo returns (0, ErrWriteTooLong) regardless
// of what the Header.Size claims.
func (tw *Writer) Write(b []byte) (int, error) {
	return 0, nil
}

// ReadFrom populates the content of the current file by reading from r.
// The bytes read must match the number of remaining bytes in the current file.
//
// If the current file is sparse and r is an io.ReadSeeker,
// then ReadFrom uses Seek to skip past holes defined in Header.SparseHoles,
// assuming that skipped regions are all NULs.
// This always reads the last byte to ensure r is the right size.
func (tw *Writer) ReadFrom(r io.Reader) (int64, error) {
	return 0, nil
}

// Close closes the tar archive by flushing the padding, and writing the footer.
// If the current file (from a prior call to WriteHeader) is not fully written,
// then this returns an error.
func (tw *Writer) Close() error {
	return nil
}
