package herdr

import (
	"os"
	"path/filepath"
	"testing"
)

// installFake puts a fake `herdr` first on PATH and returns the path of the
// file recording its argv, one invocation per line. FAKE_MODE selects behaviour.
func installFake(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
case "$1" in --version) echo "herdr 0.0.0-fake"; exit 0;; esac
if [ "$FAKE_MODE" = noserver ]; then
  echo '{"id":"x","error":{"code":"server_not_running","message":"no herdr server is running"}}'; exit 1
fi
if [ "$FAKE_MODE" = hang ]; then exec sleep 5; fi
case "$1 $2" in
"notification show")
  if [ "$FAKE_MODE" = hidden ]; then echo '{"id":"x","result":{"reason":"disabled","shown":false,"type":"notification_show"}}'
  else echo '{"id":"x","result":{"reason":"shown","shown":true,"type":"notification_show"}}'; fi;;
"agent list") echo '{"id":"x","result":{"agents":[{"agent":"claude","pane_id":"w9:p1"},{"agent":"claude","name":"worker","pane_id":"w9:p2"}],"type":"agent_list"}}';;
"api snapshot") echo '{"id":"x","result":{"snapshot":{"version":"9.9.9","protocol":22},"type":"api_snapshot"}}';;
"pane get")
  if [ "$3" = w9:p2 ]; then echo '{"id":"x","result":{"pane":{"pane_id":"w9:p2"}}}'
  else echo '{"id":"x","error":{"code":"pane_not_found","message":"no such pane"}}'; exit 1; fi;;
*) echo '{"id":"x","error":{"code":"unknown","message":"fake"}}'; exit 1;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("FAKE_MODE", "")
	return log
}

func setPane(t *testing.T, pane string) {
	t.Helper()
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", pane)
	t.Setenv("HERDR_TAB_ID", "w9:t1")
	t.Setenv("HERDR_WORKSPACE_ID", "w9")
}
