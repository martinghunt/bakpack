# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Accept `.tar`, `.tar.gz`, `.tar.bz2` and `.tar.zst` archives as annotation and genome sources for `build` and `extract`, alongside `.tar.xz`. The compression is detected from the suffix (including `.tgz`, `.tbz2` and `.tzst`), or set with `--annotations-format` / `--genomes-format`, e.g. `tar.gz`. Library users can use the new `TarSource`, whose `Compression` field selects the format.
- `reduce`, `restore` and `convert` have a batch mode: without positional arguments they process every sample in `--annotations` and `--genomes` (or `--manifest`), using the same sources as `build`, and write one file per sample to a directory or a `.tar`, `.tar.gz`, `.tar.bz2`, `.tar.xz` or `.tar.zst` file given by `--output`. Library users can call `ReduceBatch`, `RestoreBatch` and `GFF3Batch`.
- Reduced JSON named `SAMPLE.reduced.bakta.json` or `SAMPLE.reduced.json` is recognised as sample `SAMPLE`, so it pairs with that sample's genome.
- `reduce` and `build` warn when a sample's sequences cannot be matched to its genome FASTA, since those sequences and their features are left unreduced.
- `reduce` and `build` verify that each reduced JSON restores to the original before writing it.
- `convert --format genbank` and `--format embl` (and `extract --convert genbank,embl`) write GenBank and EMBL as current Bakta writes them for the JSON, checked byte for byte against Bakta 1.12.2's own writers, for JSON from any Bakta version, in default and `--compliant` mode. They are dated with the Bakta run's end, from the JSON's `run` section; JSON without it (which Bakta always writes) needs `--annotation-date`. The layout is written by faqt's `insdc` package.
- `--compliant` for `convert` and `extract --convert` writes GFF3 as Bakta's `--compliant` (Genbank/ENA/DDBJ) mode does, also in batch mode. Bakta's own `bakta_io` cannot write compliant files from JSON. Compliant output also applies the changes Bakta's `--compliant` mode makes while annotating (revised CDS/sORF product names, contigs under 200 bp left out, warnings for contig IDs and locus tags INSDC would reject), so it is compliant even when Bakta was run without `--compliant`. Library users can set `ConvertOptions.Compliant` and `Warn`.

### Changed
- **Breaking:** the `gff3` command is replaced by `convert`, which writes GFF3 (the default), GenBank or EMBL with `--format`, and extract's `--gff3` and `--gff3-annotation-only` by `--convert gff3` with `--annotation-only`. `--convert` takes several formats, e.g. `--convert gff3,genbank`; `--compliant`, `--annotation-only` and `--annotation-date` apply to them. In the library, `ConvertBaktaJSON` and `ConvertOptions` are added, `ExtractRequest`/`ExtractOptions` `GFF3` and `GFF3AnnotationOnly` are replaced by `Convert` and `ConvertOptions`, and `ExtractedSample.GFF3` by `Converted`. `BaktaGFF3` and `BaktaGFF3WithOptions` remain.
- Upgrade `github.com/martinghunt/faqt` to v0.13.0.
- `build` now reads each tar source once. Matching annotation and genome tars are no longer read twice, and a genome tar used with a non-tar annotation source or with `--order` is no longer rescanned for every sample, which made those builds slow down quadratically with sample count. Archives are byte-identical to before.
- GFF3 is now what current Bakta writes for the JSON (as its `bakta_io` does), checked byte for byte against Bakta 1.12.2's own GFF3 writer for JSON in both the current and the pre-1.10 layout. bakpack's GFF3 differed from Bakta's in several cases, now fixed: pseudogenes and truncated features (`pseudogene=unitary`/`unprocessed`/`unknown`, `pseudo=True`), `Is_circular` on circular sequences, signal peptides, contig-edge coordinates, tmRNA tag peptides, user-provided CDSs and CDS exceptions, the last CRISPR repeat, e-values of `0.0`, null rRNA/ncRNA genes, and the `# organism` line. GFF3 for features without these is unchanged.

### Fixed

- Support the Bakta `oriV` feature type (replication origins on plasmids), which previously failed with `unsupported Bakta feature type "oriV"`.
- Reduce Bakta JSON whose contigs were renamed by Bakta (run without `--keep-contig-headers`) by matching sequences to the genome FASTA via `orig_id`. Previously nothing was reduced for these files.
- Restore CDSs truncated at a contig edge whose first codon is translated literally rather than as `M`.
- Do not add derivable `stats` keys on restore when the original JSON did not have them.
- Support Bakta >= v1.10.0 JSON, which stores `sequences[]` nucleotides under `nt` and feature sequence IDs under `sequence` instead of `sequence` and `contig`. Output from older Bakta versions is still supported, and an archive chunk may mix both layouts. Reduced JSON and archives built from older-layout Bakta JSON are unchanged and remain readable by earlier bakpack releases. Samples in the newer layout are written with reduced JSON metadata version 2, which bakpack v0.4.0 and earlier reject with an unsupported version error.

## [0.4.0] - 2026-10-09

### Changed

- Upgrade `github.com/martinghunt/faqt` to v0.10.0 and require Go 1.25.

### Fixed

- Reject sample IDs that would resolve outside the output directory during extraction, preventing a crafted or corrupted archive index from writing files elsewhere on disk.
- Validate untrusted size and count fields (archive index length, chunk compressed size, sample count, feature count, and encoded list length) against the actual data before allocating, so a corrupted or malicious archive can no longer trigger an out-of-range allocation panic or unbounded memory use.
- Cap decompressed output size when reading the archive index, chunk payloads, and tar.xz source entries, so a small malicious or corrupted xz payload can no longer expand into an unbounded amount of memory (a compression bomb).
- Reject a constant-valued field codec whose recorded value doesn't match its declared type while decoding, instead of panicking on a corrupted or malicious archive index.
- Detect a truncated float64 value while decoding instead of silently zero-padding the missing bytes into a wrong number.
- Bound HTTP(S) byte-range reads to the requested length, so a misbehaving or malicious server can no longer force an unbounded amount of response data into memory.
- Write archives to a temporary file and rename it into place on success, so a build failure partway through (disk full, process killed) no longer truncates or destroys a previously existing archive at the output path.
- Preserve the exact decimal text of integer JSON numbers in canonical JSON regardless of magnitude, instead of rounding integers larger than int64 through float64, which could make distinct large integers canonicalize to the same checksum.
- Reject building from paired `.tar.xz` annotation/genome sources that contain no samples, instead of silently producing an empty archive.
- Reject non-positive sequence lengths and feature coordinates when rendering GFF3, instead of emitting invalid GFF3 lines (a feature's `start > stop` is still allowed, as that's the convention for a feature wrapping a circular contig's origin).
- Cancel the `xz` compression subprocess when a build's context is canceled, instead of letting it keep running to completion after the caller has given up, and bound how long that cancellation can be blocked by an orphaned subprocess holding xz's output pipe open (`Cmd.WaitDelay`).
- Reject an archive index that contains a duplicate sample ID or chunk ID, instead of silently keeping only the last entry.
- Report a duplicate genome sample in a `.tar.xz` genome source during build with a clear error, instead of a confusing "file not found" error from the second copy's spool file already having been removed.
- Give a GFF3 feature with neither `locus` nor `id` set a fallback ID derived from its own coordinates, instead of the constant `.`, so two such features no longer collide on the same (invalid, ambiguous) GFF3 ID.
- Reject a feature missing its contig, start, or stop when rendering GFF3, instead of silently omitting it from the output with no error.
- Reject a wraparound gap feature whose start is beyond its contig's length in `featureSpan`, matching the equivalent bounds check already in `FeatureNT`.
- Give `Genome.Contig` a pointer receiver so its lazily-built contig lookup cache actually persists across calls on a `Genome` constructed directly (rather than via `ReadGenome`), instead of silently rebuilding it from scratch every call.
- Propagate a genome FASTA write failure from `Genome.FASTABytes` as an error instead of silently discarding it, and fsync the build's temporary chunk file before reading it back so a delayed write failure (e.g. on NFS, or a filesystem near ENOSPC) is caught instead of going unnoticed.
- Insert `--` before the archive path and sample name arguments to the external `agc` command, so a sample name or path starting with `-` can't be misread as an `agc` flag (verified against the real `agc` CLI, which supports the convention).
- Derive the CLI's `reduce`/`restore`/`gff3` sample ID from the same exported `bakpack.AnnotationJSONSuffixes` list used internally for source discovery, instead of a second hardcoded copy of the suffixes that could silently drift out of sync.
- Wire up the archive chunk encoder's already-decoded-JSON reuse path, which previously always went unused: `ReduceBaktaJSON`'s result now carries the JSON object it already decoded internally, so building a chunk no longer redundantly decodes each sample's reduced JSON a second time.

### Changed

- CI now runs `go vet` and `go test -race` in addition to plain `go test`, to catch static-analysis findings and data races going forward.

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

[Unreleased]: https://github.com/martinghunt/bakpack/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/martinghunt/bakpack/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/martinghunt/bakpack/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/martinghunt/bakpack/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/martinghunt/bakpack/releases/tag/v0.1.0
