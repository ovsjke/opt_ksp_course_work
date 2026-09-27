SELECT 'users' AS table_name, count(*) AS row_count, md5(COALESCE(string_agg(to_jsonb(t)::text, E'\n' ORDER BY id), '')) AS rows_md5 FROM pavel_ovsyannikov.users t
UNION ALL
SELECT 'specialists' AS table_name, count(*) AS row_count, md5(COALESCE(string_agg(to_jsonb(t)::text, E'\n' ORDER BY id), '')) AS rows_md5 FROM pavel_ovsyannikov.specialists t
UNION ALL
SELECT 'services' AS table_name, count(*) AS row_count, md5(COALESCE(string_agg(to_jsonb(t)::text, E'\n' ORDER BY id), '')) AS rows_md5 FROM pavel_ovsyannikov.services t
UNION ALL
SELECT 'specialist_services' AS table_name, count(*) AS row_count, md5(COALESCE(string_agg(to_jsonb(t)::text, E'\n' ORDER BY specialist_id,service_id), '')) AS rows_md5 FROM pavel_ovsyannikov.specialist_services t
UNION ALL
SELECT 'slots' AS table_name, count(*) AS row_count, md5(COALESCE(string_agg(to_jsonb(t)::text, E'\n' ORDER BY id), '')) AS rows_md5 FROM pavel_ovsyannikov.slots t
UNION ALL
SELECT 'appointments' AS table_name, count(*) AS row_count, md5(COALESCE(string_agg(to_jsonb(t)::text, E'\n' ORDER BY id), '')) AS rows_md5 FROM pavel_ovsyannikov.appointments t
UNION ALL
SELECT 'sessions' AS table_name, count(*) AS row_count, md5(COALESCE(string_agg(to_jsonb(t)::text, E'\n' ORDER BY id), '')) AS rows_md5 FROM pavel_ovsyannikov.sessions t
ORDER BY table_name;
SELECT status, count(*) FROM pavel_ovsyannikov.appointments GROUP BY status ORDER BY status;
