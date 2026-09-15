# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Reject sample IDs that would resolve outside the output directory during extraction, preventing a crafted or corrupted archive index from writing files elsewhere on disk.
- Validate untrusted size and count fields (archive index length, chunk compressed size, sample count, feature count, and encoded list length) against the actual data before allocating, so a corrupted or malicious archive can no longer trigger an out-of-range allocation panic or unbounded memory use.
- Cap decompressed output size when reading the archive index, chunk payloads, and tar.xz source entries, so a small malicious or corrupted xz payload can no longer expand into an unbounded amount of memory (a compression bomb).
- Reject a constant-valued field codec whose recorded value doesn't match its declared type while decoding, instead of panicking on a corrupted or malicious archive index.
- Detect a truncated float64 value while decoding instead of silently zero-padding the missing bytes into a wrong number.
- Bound HTTP(S) byte-range reads to the requested length, so a misbehaving or malicious server can no longer force an unbounded amount of response data into memory.
- Write archives to a temporary file and rename it into place on success, so a build failure partway through (disk full, process killed) no longer truncates or destroys a previously existing archive at the output path.
- Preserve the exact decimal text of integer JSON numbers in canonical JSON regardless of magnitude, instead of rounding integers larger than int64 through float64, which could make distinct large integers canonicalize to the same checksum.

## [0.3.0] - 2026-08-11

### Added

- Let `bakpack extract --genomes` accept a standalone `.fa`, `.fasta`, or `.fna` genome file, including faqt-supported gzip, bzip2, xz, and zstd compression.
- Recognize compressed genome FASTA files when extracting from genome directories or file lists.
- Use a requested positional sample or a sole `--samples-file` entry to identify a standalone genome file, overriding its filename-derived sample ID.
- Name build temporary chunk files and annotation spool directories after their output archive, making leftovers from interrupted builds easier to identify and clean up.

## [0.2.0] - 2026-07-06

### Added

- Render Bakta-style GFF3 from original or reduced JSON with `bakpack gff3`, extract GFF3 from archives with `--gff3` / `--gff3-annotation-only`, and use the same support through the library APIs.
- Write CLI-equivalent extracted files from library code with `Archive.ExtractFiles` and the one-shot `ExtractArchive` helper.

## [0.1.0] - 2026-06-24

Initial release of `bakpack`.

### Added

- Reduce Bakta JSON by removing genome-derived fields while embedding canonical checksums.
- Restore original Bakta JSON content from reduced JSON and matching genome FASTA.
- Build `.bakpack` archives from directories, tab-delimited file lists, `.tar.xz` archives, AGC genome archives, or combined manifests.
- Extract reduced annotations, reconstructed original annotations, and genome FASTA for one or more samples.
- HTTP(S) byte-range extraction for `.bakpack` archives hosted on range-capable servers.
- Chunk-local specialized columnar archive codec with xz-compressed index and chunks.
- CLI and library APIs, including `OpenArchive` and `Archive.Extract` for local or remote archive extraction.
- Release build script and GitHub Actions release workflow.
- Read the Docs documentation with install, reduce/restore, build, extract, input source, checksum, archive format, library, and release pages.

### Notes

- Restored JSON is verified by canonical SHA-256. Object key order and whitespace are not preserved.
- `bakpack --version` prints `bakpack X.Y.Z`, normalizing release tags like `vX.Y.Z` for display.
- AGC genome extraction uses `agc getset -t 1` by default and fetches non-tar genome sources one sample at a time during extraction.
- Archive format compatibility is not guaranteed before a future stable release.

[Unreleased]: https://github.com/martinghunt/bakpack/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/martinghunt/bakpack/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/martinghunt/bakpack/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/martinghunt/bakpack/releases/tag/v0.1.0
