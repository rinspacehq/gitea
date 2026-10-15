-- Copyright 2026 The Gitea Authors. All rights reserved.
-- SPDX-License-Identifier: MIT
-- Follow-up to editor-catalog-bench.sql in the SAME disposable database.
\set ON_ERROR_STOP on
\pset tuples_only on
\pset format unaligned
SET client_min_messages = warning;
SET statement_timeout = '180s';
DO $$ BEGIN
  IF current_database() <> 'editor_catalog_bench' THEN
    RAISE EXCEPTION 'Requires the disposable editor_catalog_bench database';
  END IF;
END $$;
SET search_path = editor_catalog_probe, public;
\if :{?probe_scale}
\else
\set probe_scale 100000
\endif
SET editor_catalog_probe.scale TO :'probe_scale';

DO $$
DECLARE scale integer := current_setting('editor_catalog_probe.scale')::integer;
BEGIN
  IF scale NOT IN (10000,100000) THEN RAISE EXCEPTION 'Unsupported probe scale'; END IF;
  IF scale = 10000 THEN
    TRUNCATE editor_catalog_probe.access, editor_catalog_probe.presentation, editor_catalog_probe.repository;
    INSERT INTO repository SELECT n,99,'tags',n::text,n%97=0,n%89=0 FROM generate_series(1,scale) n;
    INSERT INTO repository SELECT scale+n,CASE WHEN n%2=0 THEN 2 ELSE 3 END,'account','project-'||n,false,false FROM generate_series(1,200) n;
    INSERT INTO presentation SELECT id,CASE WHEN id%10007=50 THEN 'General quantum 数学例子 '||id ELSE '数学理论 标签 '||id END,'tag-wiki' FROM repository WHERE owner_name='tags';
    INSERT INTO access SELECT id,2,2 FROM repository;
  END IF;
  IF (SELECT count(*) FROM repository WHERE owner_name='tags') <> scale THEN RAISE EXCEPTION 'Fixture scale differs'; END IF;
END $$;

CREATE OR REPLACE FUNCTION grams_v1(value text, width integer) RETURNS text[]
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE COST 1 SET search_path = pg_catalog
AS $$
  SELECT ARRAY(SELECT DISTINCT substr(lower(value), pos, width)
    FROM generate_series(1, char_length(lower(value))-width+1) pos)
$$;
CREATE OR REPLACE FUNCTION tokens_v1(value text) RETURNS text[]
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE COST 100 SET search_path = pg_catalog
AS $$
  SELECT editor_catalog_probe.grams_v1(value,1) || editor_catalog_probe.grams_v1(value,2) || editor_catalog_probe.grams_v1(value,3)
$$;
CREATE INDEX IF NOT EXISTS presentation_substring_tokens ON presentation USING gin (tokens_v1(title));
VACUUM ANALYZE presentation;
VACUUM ANALYZE repository;
VACUUM ANALYZE access;
CREATE TEMP TABLE measurements (scale integer, label text, iteration integer, plan jsonb);

DO $probe$
DECLARE
  label text;
  statement text;
  statements jsonb;
  plan jsonb;
  scale integer := current_setting('editor_catalog_probe.scale')::integer;
BEGIN
  statements := jsonb_build_object(
    'default_union',
    'SELECT id FROM (
      (SELECT r.id FROM repository r WHERE r.owner_id=2 AND r.owner_name<>''tags''
        AND NOT EXISTS (SELECT 1 FROM presentation p WHERE p.repo_id=r.id AND p.content_type=''tag-wiki'')
        ORDER BY r.id DESC LIMIT 25)
      UNION
      (SELECT r.id FROM repository r JOIN access a ON a.repo_id=r.id AND a.user_id=2 AND a.mode>0
        WHERE r.owner_name<>''tags''
        AND NOT EXISTS (SELECT 1 FROM presentation p WHERE p.repo_id=r.id AND p.content_type=''tag-wiki'')
        ORDER BY r.id DESC LIMIT 25)
     ) candidates ORDER BY id DESC LIMIT 25',
    'title_rare',
    'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
      WHERE tokens_v1(p.title) @> grams_v1(''quantum'',3) AND strpos(lower(p.title),''quantum'')>0
      AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
      ORDER BY r.id DESC LIMIT 25',
    'title_common_chinese_two',
    'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
      WHERE tokens_v1(p.title) @> grams_v1(''标签'',2) AND strpos(lower(p.title),''标签'')>0
      AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
      ORDER BY r.id DESC LIMIT 25',
    'title_common_chinese_one',
    'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
      WHERE tokens_v1(p.title) @> grams_v1(''数'',1) AND strpos(lower(p.title),''数'')>0
      AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
      ORDER BY r.id DESC LIMIT 25',
    'title_missing_chinese_two',
    'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
      WHERE tokens_v1(p.title) @> grams_v1(''稀词'',2) AND strpos(lower(p.title),''稀词'')>0
      AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
      ORDER BY r.id DESC LIMIT 25',
    'title_missing_chinese_one',
    'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
      WHERE tokens_v1(p.title) @> grams_v1(''罕'',1) AND strpos(lower(p.title),''罕'')>0
      AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
      ORDER BY r.id DESC LIMIT 25',
    'title_missing_latin',
    'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
      WHERE tokens_v1(p.title) @> grams_v1(''nevermatchedterm'',3) AND strpos(lower(p.title),''nevermatchedterm'')>0
      AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
      ORDER BY r.id DESC LIMIT 25',
    'title_deep_cursor',
    format('SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
      WHERE p.repo_id<%s AND r.id<%s AND tokens_v1(p.title) @> grams_v1(''标签'',2) AND strpos(lower(p.title),''标签'')>0
      AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
      ORDER BY r.id DESC LIMIT 25',scale/10,scale/10)
  );
  FOR label, statement IN SELECT * FROM jsonb_each_text(statements) LOOP
    FOR iteration IN 1..7 LOOP
      EXECUTE 'EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) '||statement INTO plan;
      INSERT INTO measurements VALUES (scale,label,iteration,plan);
    END LOOP;
  END LOOP;
END $probe$;

-- Candidate generation may overmatch repeated/reordered grams; the final
-- substring check must retain EXACT literal contains semantics.
DO $$ DECLARE q text; mismatch boolean; BEGIN
  FOREACH q IN ARRAY ARRAY['数','数学','数学理论','quantum','稀词','罕','%','_','标签 100','10050','qantum'] LOOP
    WITH sample AS MATERIALIZED (SELECT * FROM presentation ORDER BY repo_id LIMIT 1000)
    SELECT EXISTS (
      (SELECT repo_id FROM sample WHERE strpos(lower(title),lower(q))>0
       EXCEPT
       SELECT repo_id FROM sample WHERE tokens_v1(title) @> grams_v1(q,least(3,char_length(q))) AND strpos(lower(title),lower(q))>0)
      UNION ALL
      (SELECT repo_id FROM sample WHERE tokens_v1(title) @> grams_v1(q,least(3,char_length(q))) AND strpos(lower(title),lower(q))>0
       EXCEPT
       SELECT repo_id FROM sample WHERE strpos(lower(title),lower(q))>0)
    ) INTO mismatch;
    IF mismatch THEN RAISE EXCEPTION 'Substring semantics mismatch: %',q; END IF;
  END LOOP;
END $$;

SELECT jsonb_build_object(
  'kind','synthetic-index-design-probe-not-full-gitea-acceptance',
  'correctness','literal substring result sets equal on a 1000-row sample for eleven Latin, Chinese and wildcard-like inputs; performance queries use the full fixture',
  'scale',current_setting('editor_catalog_probe.scale')::integer,
  'timings','first execution and six repetitions after index build; not cold-disk measurements',
  'measurements',(SELECT jsonb_agg(to_jsonb(m) ORDER BY scale,label,iteration) FROM measurements m),
  'indexBytes',pg_relation_size('presentation_substring_tokens'),
  'tableBytes',pg_relation_size('presentation')
);
