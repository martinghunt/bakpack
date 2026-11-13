# AGC test archive

`genomes.agc` was made with `agc` 3.2.2 from `sampleA.fa` and `sampleB.fa`:

```
agc create -o genomes.agc sampleA.fa sampleB.fa
agc getset genomes.agc sampleA > sampleA.getset.fa
agc getset genomes.agc sampleB > sampleB.getset.fa
```

The tests check that bakpack's AGC reader returns exactly what `agc getset`
writes.
