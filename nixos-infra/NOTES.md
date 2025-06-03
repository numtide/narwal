AWS inventory for s3://nix-cache is stored in s3://nix-cache-inventory/nix-cache/nix-cache-inventory/

```console
$ aws s3 ls s3://nix-cache-inventory/nix-cache/nix-cache-inventory/
                           PRE 2025-05-03T01-00Z/
                           PRE 2025-05-04T01-00Z/
                           PRE 2025-05-05T01-00Z/
                           PRE 2025-05-06T01-00Z/
                           PRE 2025-05-07T01-00Z/
                           PRE 2025-05-08T01-00Z/
                           PRE 2025-05-09T01-00Z/
                           PRE 2025-05-10T01-00Z/
                           PRE 2025-05-11T01-00Z/
                           PRE 2025-05-12T01-00Z/
                           PRE 2025-05-13T01-00Z/
                           PRE 2025-05-14T01-00Z/
                           PRE 2025-05-15T01-00Z/
                           PRE 2025-05-16T01-00Z/
                           PRE 2025-05-17T01-00Z/
                           PRE 2025-05-18T01-00Z/
                           PRE 2025-05-19T01-00Z/
                           PRE 2025-05-20T01-00Z/
                           PRE 2025-05-21T01-00Z/
                           PRE 2025-05-22T01-00Z/
                           PRE 2025-05-23T01-00Z/
                           PRE 2025-05-24T01-00Z/
                           PRE 2025-05-25T01-00Z/
                           PRE 2025-05-26T01-00Z/
                           PRE 2025-05-27T01-00Z/
                           PRE 2025-05-28T01-00Z/
                           PRE 2025-05-29T01-00Z/
                           PRE 2025-05-30T01-00Z/
                           PRE 2025-05-31T01-00Z/
                           PRE 2025-06-01T01-00Z/
                           PRE 2025-06-02T01-00Z/
                           PRE data/
                           PRE hive/
```

Each date has it's own metadata:

```console
$ aws s3 ls s3://nix-cache-inventory/nix-cache/nix-cache-inventory/2025-06-02T01-00Z/
2025-06-02 23:55:44         33 manifest.checksum
2025-06-02 23:55:44      93601 manifest.json
$ aws s3 cp s3://nix-cache-inventory/nix-cache/nix-cache-inventory/2025-06-02T01-00Z/manifest.json -
{
  "sourceBucket" : "nix-cache",
  "destinationBucket" : "arn:aws:s3:::nix-cache-inventory",
  "version" : "2016-11-30",
  "creationTimestamp" : "1748826000000",
  "fileFormat" : "Parquet",
  "fileSchema" : "message s3.inventory {  required binary bucket (STRING);  required binary key (STRING);  optional int64 size;  optional int64 last_modified_date (TIMESTAMP(MILLIS,true));  optional binary e_tag (STRING);  optional binary storage_class (STRING);}",
  "files" : [ {
    "key" : "nix-cache/nix-cache-inventory/data/ed88423e-6457-4a45-ad18-6e13b66d076f.parquet",
    "size" : 176058964,
    "MD5checksum" : "55bf7c536ac7b6de3c79ff4520a0fa7d"
  }, {
    "key" : "nix-cache/nix-cache-inventory/data/858aa1a6-0b23-4f6d-9d9f-b6414b38f7c4.parquet",
    "size" : 58644614,
    "MD5checksum" : "dadc5596c0efc4863ac2eec86cbab1f0"
  }, {
    "key" : "nix-cache/nix-cache-inventory/data/fa16ab13-bdd8-4f69-af98-2a0940ada45d.parquet",
    "size" : 176054504,
    "MD5checksum" : "0b75d70644e6d47cb99e3c33c597ebef"
  },
  ...<snip>
```
