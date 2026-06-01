-- Surface the Parquet file's KV footer metadata.
--
-- xk6-output-parquet writes provenance keys (test_run_id, schema_version,
-- xk6_output_parquet.*) into the file footer — see pkg/parquet/metadata.go.
-- This query documents what's discoverable per file without scanning rows.
--
-- parquet_kv_metadata returns key/value as BLOB; decode() casts them to
-- UTF-8 strings. The script_options key (full lib.Options JSON) is excluded
-- here because it's large — query it explicitly when you need it:
--
--   SELECT decode(value) FROM parquet_kv_metadata('run.parquet')
--   WHERE decode(key) = 'xk6_output_parquet.script_options';
SELECT
    decode(key)   AS key,
    decode(value) AS value
FROM parquet_kv_metadata(getenv('PARQUET_FILE'))
WHERE decode(key) LIKE 'xk6_output_parquet.%'
  AND decode(key) != 'xk6_output_parquet.script_options'
ORDER BY key;
