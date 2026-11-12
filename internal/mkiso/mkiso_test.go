package mkiso

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

type spec struct {
	disk   string // path in the source folder; "name/" suffix = directory
	want   string // expected path in the image (default: disk)
	data   []byte
	sparse int64
}

func randBytes(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

func writeTree(t *testing.T, root string, specs []spec) (map[string]fileSum, []string) {
	t.Helper()
	want := map[string]fileSum{}
	var dirs []string
	for _, s := range specs {
		full := filepath.Join(root, filepath.FromSlash(s.disk))
		if strings.HasSuffix(s.disk, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			w := s.want
			if w == "" {
				w = strings.TrimSuffix(s.disk, "/")
			}
			dirs = append(dirs, w)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if s.sparse > 0 {
			f, err := os.Create(full)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Truncate(s.sparse); err != nil {
				t.Fatal(err)
			}
			f.Close()
		} else if err := os.WriteFile(full, s.data, 0o644); err != nil {
			t.Fatal(err)
		}
		w := s.want
		if w == "" {
			w = s.disk
		}
		f, _ := os.Open(full)
		h := sha256.New()
		n, _ := io.Copy(h, f)
		f.Close()
		fs := fileSum{size: n}
		copy(fs.sum[:], h.Sum(nil))
		want[w] = fs
	}
	return want, dirs
}

func buildToFile(t *testing.T, dir string, opt Options) (string, Info, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "out.iso")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := Build(context.Background(), dir, f, opt)
	return p, info, err
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"plain.txt":       "plain.txt",
		"a:b":             "a_b",
		"q?r*s":           "q_r_s",
		`a\b"c<d>e|f`:     "a_b_c_d_e_f",
		"semi;colon":      "semi_colon",
		"foo.":            "foo_",
		"foo ":            "foo_",
		"a..":             "a__",
		"tab\there":       "tab_here",
		"Ärger":          "Ärger", // NFD -> NFC
		"über":            "über",
		"inner space.txt": "inner space.txt",
		"del\x7fchar":     "del_char",
		"bad\xffutf8":     "bad_utf8",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildPlain(t *testing.T) {
	src := filepath.Join(t.TempDir(), "myfolder")
	long := strings.Repeat("l", 99) + ".txt" // 103 units
	specs := []spec{
		{disk: "readme.txt", data: []byte("hello world")},
		{disk: "empty.bin", data: nil},
		{disk: "docs/Übersicht Äpfel.txt", data: randBytes(1, 3000)},
		{disk: "nfd/Ä.txt", want: "nfd/Ä.txt", data: randBytes(2, 10)},
		{disk: "sp/semi;colon.txt", want: "sp/semi_colon.txt", data: randBytes(5, 5)},
		{disk: "sizes/s2047", data: randBytes(7, 2047)},
		{disk: "sizes/s2048", data: randBytes(8, 2048)},
		{disk: "sizes/s2049", data: randBytes(9, 2049)},
		{disk: "sizes/s100000", data: randBytes(10, 100000)},
		{disk: long, data: randBytes(11, 77)},
		{disk: "deep/a/b/c/d.txt", data: randBytes(12, 4096)},
		{disk: "many/Long File Name One.txt", data: randBytes(13, 1)},
		{disk: "many/Long File Name Two.txt", data: randBytes(14, 1)},
		{disk: "many/Long File Name Three.txt", data: randBytes(15, 1)},
		{disk: "many/abcdefghi.txt", data: randBytes(16, 1)},
		{disk: "many/abcdefghj.txt", data: randBytes(17, 1)},
		{disk: "emptydir/"},
	}
	wantDirs := 11 // root excluded
	if runtime.GOOS != "windows" {
		// Windows forbids ':', '?' and '*' in names (on NTFS "a:b" names a stream of the file
		// "a") and drops a trailing dot, so these names cannot exist there; TestSanitizeName
		// covers their mapping on every system.
		specs = append(specs,
			spec{disk: "sp/a:b?.txt", want: "sp/a_b_.txt", data: randBytes(3, 5)},
			spec{disk: "sp/x*y.txt", want: "sp/x_y.txt", data: randBytes(4, 5)},
			spec{disk: "trail/foo.", want: "trail/foo_", data: randBytes(6, 5)},
		)
	} else {
		wantDirs-- // no "trail"
	}
	want, dirs := writeTree(t, src, specs)
	var progress [][2]int64
	path, info, err := buildToFile(t, src, Options{Progress: func(d, tot int64) { progress = append(progress, [2]int64{d, tot}) }})
	if err != nil {
		t.Fatal(err)
	}
	if info.Bootable {
		t.Fatal("must not be bootable")
	}
	if info.Files != len(want) {
		t.Fatalf("Info.Files = %d, want %d", info.Files, len(want))
	}
	if info.Dirs != wantDirs {
		t.Fatalf("Info.Dirs = %d, want %d", info.Dirs, wantDirs)
	}
	verifyISO(t, path, want, verifyOpts{pvdVolume: "myfolder", jolVolume: "myfolder", extraDirs: dirs, isoSizeInf: info.Size})
	if len(progress) == 0 {
		t.Fatal("no progress")
	}
	var total int64
	for _, w := range want {
		total += w.size
	}
	last := progress[len(progress)-1]
	if last[0] != total || last[1] != total {
		t.Fatalf("last progress %v, want (%d,%d)", last, total, total)
	}
	for i := 1; i < len(progress); i++ {
		if progress[i][0] < progress[i-1][0] {
			t.Fatal("progress not monotonic")
		}
	}
}

func TestVolumeIDs(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, []spec{{disk: "a.txt", data: []byte("x")}})
	path, _, err := buildToFile(t, src, Options{VolumeID: "Test Vol Ä and a very long name over 32 chars"})
	if err != nil {
		t.Fatal(err)
	}
	verifyISO(t, path, map[string]fileSum{"a.txt": sum([]byte("x"))}, verifyOpts{
		pvdVolume: "Test Vol _ and a very long name",
		jolVolume: "Test Vol Ä and a",
	})
}

func sum(b []byte) fileSum {
	return fileSum{size: int64(len(b)), sum: sha256.Sum256(b)}
}

func TestBuildBootable(t *testing.T) {
	src := filepath.Join(t.TempDir(), "efi")
	want, dirs := writeTree(t, src, []spec{
		{disk: "EFI/boot/Bootx64.EFI", data: randBytes(20, 5000)},
		{disk: "EFI/Microsoft/note.txt", data: []byte("note")},
		{disk: "startup.nsh", data: []byte("echo hi\n")},
		{disk: "big.bin", data: randBytes(21, 3<<20+17)},
		{disk: "empty.txt"},
		{disk: "Long File Name One.txt", data: randBytes(22, 100)},
		{disk: "Long File Name Two.txt", data: randBytes(23, 2048)},
		{disk: "Long File Name Three.txt", data: randBytes(24, 2049)},
		{disk: "abcdefghi.txt", data: randBytes(25, 100)},
		{disk: "abcdefghj.txt", data: randBytes(26, 100)},
		{disk: "readme.txt", data: randBytes(27, 100)},
		{disk: "Ümlaut äöü.txt", data: randBytes(28, 100)},
		{disk: "UPPER.TXT", data: randBytes(29, 100)},
		{disk: "a.b.c.d", data: randBytes(30, 100)},
		{disk: "sub/dir/with spaces/file.dat", data: randBytes(31, 9000)},
		{disk: "emptydir/"},
	})
	path, info, err := buildToFile(t, src, Options{VolumeID: "Efi Test"})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Bootable {
		t.Fatal("expected Bootable")
	}
	verifyISO(t, path, want, verifyOpts{boot: true, extraDirs: dirs, pvdVolume: "Efi Test", fatLabel: "EFI TEST", checkFsck: true, isoSizeInf: info.Size})
	st, _ := os.Stat(path)
	if st.Size() < 128<<20 {
		t.Fatalf("bootable image only %d bytes", st.Size())
	}
}

func TestBootableDetection(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		want bool
	}{
		"upper":      {"EFI/BOOT/BOOTX64.EFI", true},
		"lower":      {"efi/boot/bootx64.efi", true},
		"mixed":      {"Efi/Boot/BootX64.Efi", true},
		"other file": {"EFI/BOOT/BOOTIA32.EFI", false},
		"wrong dir":  {"EFI/BOOTX64.EFI", false},
		"root file":  {"BOOTX64.EFI", false},
	} {
		t.Run(name, func(t *testing.T) {
			src := t.TempDir()
			writeTree(t, src, []spec{{disk: tc.file, data: []byte("x")}})
			_, info, err := buildToFile(t, src, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if info.Bootable != tc.want {
				t.Fatalf("Bootable = %v", info.Bootable)
			}
		})
	}
}

func TestBootableLargerThanMinimum(t *testing.T) {
	src := t.TempDir()
	want, _ := writeTree(t, src, []spec{
		{disk: "EFI/BOOT/BOOTX64.EFI", data: randBytes(40, 1000)},
		{disk: "zero.bin", sparse: 129 << 20},
	})
	path, info, err := buildToFile(t, src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	verifyISO(t, path, want, verifyOpts{boot: true, checkFsck: true, isoSizeInf: info.Size})
}

func TestFsckFat(t *testing.T) {
	if findFsck() == "" {
		t.Skip("fsck.fat not available")
	}
	src := t.TempDir()
	var specs []spec
	specs = append(specs, spec{disk: "EFI/BOOT/BOOTX64.EFI", data: randBytes(50, 3000)})
	for i := 0; i < 40; i++ {
		specs = append(specs, spec{disk: fmt.Sprintf("Some Long Directory Name/Another Long File Name %02d.txt", i), data: randBytes(int64(100+i), 100+i*500)})
	}
	want, _ := writeTree(t, src, specs)
	path, _, err := buildToFile(t, src, Options{VolumeID: "fsck"})
	if err != nil {
		t.Fatal(err)
	}
	verifyISO(t, path, want, verifyOpts{boot: true, fatLabel: "FSCK"})
	f, _ := os.Open(path)
	defer f.Close()
	rba, _ := checkCatalog(t, f)
	st, _ := f.Stat()
	img := extractImage(t, path, int64(rba)*2048, st.Size()-int64(rba)*2048)
	out, err := execFsck(img)
	if err != nil {
		t.Fatalf("fsck.fat: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "files") {
		t.Fatalf("unexpected fsck output: %s", out)
	}
}

// requireNames skips the test unless dir holds exactly the entries names. A conflict test needs
// two names the file system keeps apart: a case-insensitive one (APFS, NTFS) stores "A.txt" and
// "a.TXT" as one file, and on NTFS "a:b" names a stream of the file "a".
func requireNames(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name()
	}
	want := slices.Clone(names)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Skipf("the file system cannot keep the names %q apart (found %q)", names, got)
	}
}

func TestErrors(t *testing.T) {
	asPath := func(t *testing.T, err error) *PathError {
		t.Helper()
		var pe *PathError
		if !errors.As(err, &pe) {
			t.Fatalf("not a *PathError: %v", err)
		}
		return pe
	}
	t.Run("empty", func(t *testing.T) {
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, "sub", "sub2"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, _, err := buildToFile(t, src, Options{})
		if !errors.Is(err, ErrEmpty) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("conflict case", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, []spec{{disk: "d/A.txt", data: []byte("1")}, {disk: "d/a.TXT", data: []byte("2")}})
		requireNames(t, filepath.Join(src, "d"), "A.txt", "a.TXT")
		_, _, err := buildToFile(t, src, Options{})
		if !errors.Is(err, ErrNameConflict) {
			t.Fatalf("err = %v", err)
		}
		if p := asPath(t, err).Path; !strings.HasPrefix(p, "d/") {
			t.Fatalf("path %q", p)
		}
	})
	t.Run("conflict sanitized", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, []spec{{disk: "a:b", data: []byte("1")}, {disk: "a_b", data: []byte("2")}})
		requireNames(t, src, "a:b", "a_b")
		_, _, err := buildToFile(t, src, Options{})
		if !errors.Is(err, ErrNameConflict) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("name 103 ok 104 too long", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, []spec{{disk: strings.Repeat("n", 103), data: []byte("1")}, {disk: strings.Repeat("ä", 103) + "/f", data: []byte("1")}})
		path, _, err := buildToFile(t, src, Options{})
		if err != nil {
			t.Fatal(err)
		}
		verifyISO(t, path, map[string]fileSum{strings.Repeat("n", 103): sum([]byte("1")), strings.Repeat("ä", 103) + "/f": sum([]byte("1"))}, verifyOpts{})

		src2 := t.TempDir()
		writeTree(t, src2, []spec{{disk: "x/" + strings.Repeat("n", 104), data: []byte("1")}})
		_, _, err = buildToFile(t, src2, Options{})
		if !errors.Is(err, ErrNameTooLong) {
			t.Fatalf("err = %v", err)
		}
		if p := asPath(t, err).Path; p != "x/"+strings.Repeat("n", 104) {
			t.Fatalf("path %q", p)
		}
		src3 := t.TempDir()
		writeTree(t, src3, []spec{{disk: strings.Repeat("d", 104) + "/f", data: []byte("1")}})
		if _, _, err = buildToFile(t, src3, Options{}); !errors.Is(err, ErrNameTooLong) {
			t.Fatalf("dir: err = %v", err)
		}
		// 52 astral characters are 104 UTF-16 units
		src4 := t.TempDir()
		writeTree(t, src4, []spec{{disk: strings.Repeat("\U0001F600", 52), data: []byte("1")}})
		if _, _, err = buildToFile(t, src4, Options{}); !errors.Is(err, ErrNameTooLong) {
			t.Fatalf("astral: err = %v", err)
		}
	})
	t.Run("file too large", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, []spec{{disk: "small", data: []byte("1")}, {disk: "sub/huge.wim", sparse: 4 << 30}})
		_, _, err := buildToFile(t, src, Options{})
		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("err = %v", err)
		}
		if p := asPath(t, err).Path; p != "sub/huge.wim" {
			t.Fatalf("path %q", p)
		}
	})
	t.Run("size changes", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, []spec{{disk: "a", data: randBytes(60, 3<<20)}, {disk: "b", data: randBytes(61, 1000)}})
		p := filepath.Join(t.TempDir(), "o.iso")
		f, _ := os.Create(p)
		defer f.Close()
		grown := false
		_, err := Build(context.Background(), src, f, Options{Progress: func(d, tot int64) {
			if !grown {
				grown = true
				g, _ := os.OpenFile(filepath.Join(src, "b"), os.O_APPEND|os.O_WRONLY, 0)
				g.WriteString("more")
				g.Close()
			}
		}})
		if !errors.Is(err, ErrFileChanged) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSymlinkSkipped(t *testing.T) {
	src := t.TempDir()
	want, _ := writeTree(t, src, []spec{{disk: "real.txt", data: []byte("data")}, {disk: "d/x.txt", data: []byte("x")}})
	if err := os.Symlink("real.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	if err := os.Symlink("d", filepath.Join(src, "dlink")); err != nil {
		t.Skip(err)
	}
	path, info, err := buildToFile(t, src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(info.Skipped)
	if fmt.Sprint(info.Skipped) != "[dlink link.txt]" {
		t.Fatalf("Skipped = %v", info.Skipped)
	}
	verifyISO(t, path, want, verifyOpts{})
}

func cacheEnv(t *testing.T) string {
	t.Helper()
	c := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", c) // Linux
	t.Setenv("HOME", c)           // macOS
	t.Setenv("LocalAppData", c)   // Windows
	return c
}

func isoFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.iso"))
	return m
}

func TestBuildTempAndClose(t *testing.T) {
	cacheEnv(t)
	src := t.TempDir()
	want, _ := writeTree(t, src, []spec{{disk: "a.txt", data: randBytes(70, 5000)}})
	img, info, err := BuildTemp(context.Background(), src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Close also when a check fails: on Windows an open file cannot be removed, and the
	// TempDir cleanup of cacheEnv would fail on it.
	t.Cleanup(func() { img.Close() })
	cd, err := CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(img.Path()) != cd || !strings.HasSuffix(cd, filepath.Join("OpenLAMT", "ider")) {
		t.Fatalf("path %q, cache %q", img.Path(), cd)
	}
	st, err := os.Stat(cd)
	if err != nil || !st.IsDir() {
		t.Fatalf("cache dir: %v %v", st, err)
	}
	// Windows reports no Unix permission bits; MkdirAll ignores the mode there.
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir mode %v, want 0700", st.Mode().Perm())
	}
	if img.Size() != info.Size {
		t.Fatalf("Size %d != %d", img.Size(), info.Size)
	}
	b := make([]byte, 5)
	if _, err := img.ReadAt(b, 16*2048+1); err != nil || string(b) != "CD001" {
		t.Fatalf("ReadAt: %q %v", b, err)
	}
	verifyISO(t, img.Path(), want, verifyOpts{})
	p := img.Path()
	if err := img.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
	if err := img.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestBuildTempErrorRemovesFile(t *testing.T) {
	cacheEnv(t)
	src := t.TempDir()
	if _, _, err := BuildTemp(context.Background(), src, Options{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v", err)
	}
	cd, _ := CacheDir()
	if m := isoFiles(t, cd); len(m) != 0 {
		t.Fatalf("leftover %v", m)
	}
}

func TestCancel(t *testing.T) {
	cacheEnv(t)
	src := t.TempDir()
	writeTree(t, src, []spec{{disk: "a", data: randBytes(80, 6<<20)}, {disk: "b", data: randBytes(81, 100)}})
	cd, _ := CacheDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := BuildTemp(ctx, src, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled: %v", err)
	}
	if m := isoFiles(t, cd); len(m) != 0 {
		t.Fatalf("leftover %v", m)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	_, _, err := BuildTemp(ctx, src, Options{Progress: func(d, tot int64) {
		calls++
		if calls == 2 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-build cancel: %v", err)
	}
	if calls > 3 {
		t.Fatalf("kept copying after cancel: %d progress calls", calls)
	}
	if m := isoFiles(t, cd); len(m) != 0 {
		t.Fatalf("leftover %v", m)
	}
}

func TestCleanCache(t *testing.T) {
	cacheEnv(t)
	cd, err := CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.iso", "b.iso", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(cd, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := CleanCache(); err != nil {
		t.Fatal(err)
	}
	if m := isoFiles(t, cd); len(m) != 0 {
		t.Fatalf("not cleaned: %v", m)
	}
	if _, err := os.Stat(filepath.Join(cd, "keep.txt")); err != nil {
		t.Fatal("keep.txt must stay")
	}
	if err := CleanCache(); err != nil {
		t.Fatal(err)
	}
}

func TestTooManyEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("creates ~66000 files")
	}
	t.Run("fat directory", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, []spec{{disk: "EFI/BOOT/BOOTX64.EFI", data: []byte("x")}})
		for i := 0; i < 65536; i++ {
			if err := os.WriteFile(filepath.Join(src, fmt.Sprintf("F%07d.TXT", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, _, err := buildToFile(t, src, Options{})
		if !errors.Is(err, ErrDirTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("directories", func(t *testing.T) {
		src := t.TempDir()
		writeTree(t, src, []spec{{disk: "f.txt", data: []byte("x")}})
		for i := 0; i < 300; i++ {
			for j := 0; j < 220; j++ {
				if err := os.MkdirAll(filepath.Join(src, fmt.Sprintf("d%03d", i), fmt.Sprintf("e%03d", j)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
		_, _, err := buildToFile(t, src, Options{})
		if !errors.Is(err, ErrTooManyDirs) {
			t.Fatalf("err = %v", err)
		}
	})
}
