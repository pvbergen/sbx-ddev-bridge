package sqlread

import "testing"

func TestCheck(t *testing.T) {
	reads := []string{
		"SELECT 1",
		"select name from users_field_data where uid = 1;",
		"SELECT 1; SELECT 2;",
		"SELECT COUNT(*) FROM node; SHOW TABLES; DESCRIBE users; DESC node; EXPLAIN SELECT 1",
		"  \n SELECT 1 ;; ; SELECT 2",
		"(SELECT 1) UNION (SELECT 2)",
		"SELECT 'a;DELETE FROM x' AS s",
		`SELECT "it\"s; DROP TABLE x"`,
		"SELECT 'it''s; DROP TABLE x'",
		"SELECT `into`, `lock` FROM `for`",
		"SELECT 1 -- ; DELETE FROM x\n; SELECT 2",
		"SELECT 1 # ; DELETE FROM x\n",
		"/* note; DELETE */ SELECT 1",
		"SELECT /*+ MAX_EXECUTION_TIME(1000) */ 1",
		"SELECT 5--3",
		"SELECT REPLACE(name, 'a', 'b'), updated FROM t",
		"SHOW CREATE TABLE node",
		"SELECT @x",
	}
	for _, q := range reads {
		if r := Check(q); r != "" {
			t.Errorf("Check(%q) = %q, want a read", q, r)
		}
	}

	writes := []string{
		"",
		"  ; -- nothing\n",
		"DELETE FROM users",
		"SELECT 1; DELETE FROM users",
		"SELECT 1; delete from users;",
		"UPDATE users SET name = 'x'",
		"INSERT INTO t VALUES (1)",
		"WITH c AS (SELECT 1) DELETE FROM t",
		"WITH c AS (SELECT 1) SELECT * FROM c",
		"SET @x = 1",
		"TRUNCATE cache_render",
		"SELECT * FROM t INTO OUTFILE '/tmp/x'",
		"SELECT 1 INTO @x",
		"SELECT * FROM t FOR UPDATE",
		"SELECT * FROM t FOR SHARE",
		"SELECT * FROM t LOCK IN SHARE MODE",
		"EXPLAIN ANALYZE SELECT 1",
		"/*! DELETE FROM t */ SELECT 1",
		"SELECT 1 /*!50000 , SLEEP(1) */",
		"SELECT 1 /*M! , 2 */",
		"SELECT 1 \\! rm -rf /",
		"SELECT 1 \\G",
		"SELECT 'unterminated",
		"SELECT 1 /* unterminated",
		"'x' SELECT 1",
		"@x := 1",
		"SELECT 1; 'x'",
		"--x SELECT 1",
	}
	for _, q := range writes {
		if r := Check(q); r == "" {
			t.Errorf("Check(%q) passed as a read", q)
		}
	}
}
