-- Copyright 2026 The Gitea Authors. All rights reserved.
-- SPDX-License-Identifier: MIT
-- Synthetic query-design probe ONLY; never run against an application database.
\set ON_ERROR_STOP on
\pset tuples_only on
\pset format unaligned
SET client_min_messages = warning;
SET statement_timeout = '90s';
DO $$ BEGIN
  IF current_database() <> 'editor_catalog_bench' THEN
    RAISE EXCEPTION 'Requires the disposable editor_catalog_bench database';
  END IF;
END $$;
CREATE SCHEMA editor_catalog_probe;
SET search_path = editor_catalog_probe, public;
CREATE EXTENSION pg_trgm;
CREATE TABLE repository (
  id bigint PRIMARY KEY, owner_id bigint NOT NULL, owner_name text NOT NULL,
  lower_name text NOT NULL, is_private boolean NOT NULL, is_archived boolean NOT NULL
);
CREATE UNIQUE INDEX repo_owner_name ON repository (owner_id, lower_name);
CREATE INDEX ordinary_candidates ON repository (id DESC) WHERE owner_name <> 'tags';
CREATE INDEX public_tags ON repository (id DESC) WHERE owner_name = 'tags' AND NOT is_private AND NOT is_archived;
CREATE TABLE presentation (repo_id bigint PRIMARY KEY, title text NOT NULL, content_type text NOT NULL);
CREATE INDEX presentation_type ON presentation (content_type, repo_id);
CREATE INDEX presentation_title_search ON presentation USING gin (lower(title) gin_trgm_ops);
CREATE TABLE access (repo_id bigint NOT NULL, user_id bigint NOT NULL, mode integer NOT NULL, PRIMARY KEY(user_id, repo_id));
CREATE TEMP TABLE measurements (scale integer, label text, iteration integer, plan jsonb);
CREATE TEMP TABLE index_sizes (scale integer, name text, bytes bigint);

DO $probe$
DECLARE
  scale integer;
  label text;
  statement text;
  statements jsonb;
  plan jsonb;
BEGIN
  FOREACH scale IN ARRAY ARRAY[10000,100000] LOOP
    TRUNCATE editor_catalog_probe.access, editor_catalog_probe.presentation, editor_catalog_probe.repository;
    INSERT INTO repository
      SELECT n, 99, 'tags', n::text, n % 97 = 0, n % 89 = 0 FROM generate_series(1, scale) n;
    INSERT INTO repository
      SELECT scale+n, CASE WHEN n % 2 = 0 THEN 2 ELSE 3 END, 'account', 'project-'||n, false, false
      FROM generate_series(1, 200) n;
    INSERT INTO presentation
      SELECT id, CASE WHEN id % 10007 = 50 THEN 'General quantum 数学例子 '||id ELSE '数学理论 标签 '||id END, 'tag-wiki'
      FROM repository WHERE owner_name = 'tags';
    -- Include tags in every account membership: the old caller would have to
    -- transfer/filter all of these before discovering its ordinary projects.
    INSERT INTO access SELECT id, 2, 2 FROM repository;
    ANALYZE repository; ANALYZE presentation; ANALYZE access;

    statements := jsonb_build_object(
      'default_first',
      'SELECT r.id FROM repository r WHERE r.owner_name <> ''tags'' AND
        NOT EXISTS (SELECT 1 FROM presentation p WHERE p.repo_id=r.id AND p.content_type=''tag-wiki'') AND
        (r.owner_id=2 OR EXISTS (SELECT 1 FROM access a WHERE a.user_id=2 AND a.repo_id=r.id AND a.mode>0))
        ORDER BY r.id DESC LIMIT 25',
      'default_cursor',
      format('SELECT r.id FROM repository r WHERE r.owner_name <> ''tags'' AND r.id < %s AND
        NOT EXISTS (SELECT 1 FROM presentation p WHERE p.repo_id=r.id AND p.content_type=''tag-wiki'') AND
        (r.owner_id=2 OR EXISTS (SELECT 1 FROM access a WHERE a.user_id=2 AND a.repo_id=r.id AND a.mode>0))
        ORDER BY r.id DESC LIMIT 25', scale+30),
      'title_rare',
      'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
        WHERE lower(p.title) LIKE ''%quantum%'' AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25',
      'title_common_chinese_two',
      'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
        WHERE lower(p.title) LIKE ''%标签%'' AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25',
      'title_common_chinese_one',
      'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
        WHERE lower(p.title) LIKE ''%数%'' AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25',
      'title_missing_chinese_two',
      'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
        WHERE lower(p.title) LIKE ''%稀词%'' AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25',
      'title_missing_chinese_one',
      'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
        WHERE lower(p.title) LIKE ''%罕%'' AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25',
      'title_missing_latin',
      'SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
        WHERE lower(p.title) LIKE ''%nevermatchedterm%'' AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25',
      'exact_coordinate',
      'SELECT r.id FROM repository r WHERE r.owner_id=99 AND r.lower_name=''50'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25',
      'title_deep_cursor',
      format('SELECT r.id FROM presentation p JOIN repository r ON r.id=p.repo_id
        WHERE lower(p.title) LIKE ''%%标签%%'' AND r.id < %s AND r.owner_name=''tags'' AND NOT r.is_private AND NOT r.is_archived
        ORDER BY r.id DESC LIMIT 25', scale/10)
    );
    FOR label, statement IN SELECT * FROM jsonb_each_text(statements) LOOP
      FOR iteration IN 1..7 LOOP
        EXECUTE 'EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) '||statement INTO plan;
        INSERT INTO measurements VALUES (scale, label, iteration, plan);
      END LOOP;
    END LOOP;
    INSERT INTO index_sizes SELECT scale, c.relname, pg_relation_size(c.oid)
      FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
      WHERE n.nspname='editor_catalog_probe' AND c.relkind='i';
  END LOOP;
END $probe$;
SELECT jsonb_build_object(
  'kind','synthetic-query-design-probe-not-full-gitea-acceptance',
  'postgres',current_setting('server_version'),
  'timings','first execution and six repetitions after fixture load; not cold-disk measurements',
  'measurements',(SELECT jsonb_agg(to_jsonb(m) ORDER BY scale,label,iteration) FROM measurements m),
  'indexSizes',(SELECT jsonb_agg(to_jsonb(i) ORDER BY scale,name) FROM index_sizes i)
);
