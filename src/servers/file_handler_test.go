package servers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSafePathAllowsDotDotPrefix(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "..notes"), 0o755))

	got, err := getSafePath(root, filepath.Join("..notes", "video.flv"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "..notes", "video.flv"), got)
}

func TestGetSafePathRejectsTraversal(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "recordings")
	require.NoError(t, os.Mkdir(root, 0o755))

	for _, candidate := range []string{
		"../outside.flv",
		filepath.Join("sub", "..", "..", "outside.flv"),
		filepath.Join(parent, "outside.flv"),
	} {
		t.Run(candidate, func(t *testing.T) {
			_, err := getSafePath(root, candidate)
			require.Error(t, err)
		})
	}
}

func TestValidateEntryName(t *testing.T) {
	for _, name := range []string{"..notes", "直播录像", "video.flv"} {
		assert.NoError(t, validateEntryName(name), name)
	}
	for _, name := range []string{"", ".", "..", "../outside", `sub\file`} {
		assert.Error(t, validateEntryName(name), name)
	}
}

func TestGetSafePathRejectsSymlinkOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "recordings")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.Mkdir(root, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))
	link := filepath.Join(root, "linked")
	createDirectoryLink(t, outside, link)

	_, err := getSafePath(root, filepath.Join(filepath.Base(link), "secret.txt"))
	require.Error(t, err)
}

func TestOutputFileHandlerRejectsSymlinkOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "recordings")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.Mkdir(root, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))
	createDirectoryLink(t, outside, filepath.Join(root, "linked"))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/linked/secret.txt", nil)
	newOutputFileHandler(root).ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func createDirectoryLink(t *testing.T, target, link string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		output, err := exec.Command("cmd", "/C", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Skipf("无法创建 junction: %v: %s", err, output)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		return
	}

	require.NoError(t, os.Symlink(target, link))
}
