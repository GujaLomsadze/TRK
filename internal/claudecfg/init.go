package claudecfg

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Paths struct{ Settings, ClaudeMD string }

func DefaultPaths() (Paths, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(home, ".claude")
	}
	return Paths{Settings: filepath.Join(dir, "settings.json"), ClaudeMD: filepath.Join(dir, "CLAUDE.md")}, nil
}

type InitOptions struct {
	Paths          Paths
	TrkCmd         string
	ChainStatus    func(existing string) bool
	SkipStatusLine bool
	SkipClaudeMD   bool
	DryRun         bool
	Now            time.Time
}

type InitResult struct {
	HooksChanged    bool
	Status          StatusAction
	ExistingStatus  string
	SettingsBackup  string
	ClaudeMDChanged bool
	ClaudeMDBackup  string
}

func Init(o InitOptions) (InitResult, error) {
	var res InitResult
	raw, err := os.ReadFile(o.Paths.Settings)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	existed := err == nil
	root, err := Parse(raw)
	if err != nil {
		return res, errors.New(o.Paths.Settings + ": " + err.Error() + " (nothing was changed)")
	}
	if res.HooksChanged, err = MergeHooks(root, o.TrkCmd); err != nil {
		return res, err
	}
	if !o.SkipStatusLine {
		res.ExistingStatus = ExistingStatusCommand(root)
		chain := false
		if res.ExistingStatus != "" && !trkStatusRe.MatchString(res.ExistingStatus) && o.ChainStatus != nil {
			chain = o.ChainStatus(res.ExistingStatus)
		}
		if res.Status, err = MergeStatusLine(root, o.TrkCmd, chain); err != nil {
			return res, err
		}
	}
	if (res.HooksChanged || res.Status == StatusAdded || res.Status == StatusChained) && !o.DryRun {
		out, err := root.Marshal()
		if err != nil {
			return res, err
		}
		if existed {
			if res.SettingsBackup, err = backup(o.Paths.Settings, raw, o.Now); err != nil {
				return res, err
			}
		}
		if err := writeAtomic(o.Paths.Settings, out); err != nil {
			return res, err
		}
	}
	if o.SkipClaudeMD {
		return res, nil
	}
	md, err := os.ReadFile(o.Paths.ClaudeMD)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	mdExisted := err == nil
	updated := ApplyBlock(string(md))
	res.ClaudeMDChanged = updated != string(md)
	if res.ClaudeMDChanged && !o.DryRun {
		if mdExisted {
			if res.ClaudeMDBackup, err = backup(o.Paths.ClaudeMD, md, o.Now); err != nil {
				return res, err
			}
		}
		if err := writeAtomic(o.Paths.ClaudeMD, []byte(updated)); err != nil {
			return res, err
		}
	}
	return res, nil
}

func backup(path string, data []byte, now time.Time) (string, error) {
	dst := path + ".trk-backup-" + now.Format("20060102-150405")
	return dst, os.WriteFile(dst, data, 0o600)
}

// writeAtomic writes via temp file + rename, following symlinks (dotfile setups) and keeping the mode.
func writeAtomic(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".trk-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
