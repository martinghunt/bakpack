// Package bakpack provides tools for reducing, packing, reading, and
// extracting Bakta annotation JSON archives.
//
// Use OpenArchive with Archive.Extract when callers want extracted bytes in
// memory, Archive.ExtractFiles when callers want standard bakpack output files
// in a directory, and ExtractArchive for one-shot file extraction from an
// archive path or HTTP(S) URL.
package bakpack
