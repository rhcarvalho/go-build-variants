// +build ignore

package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var versions = []string{
	"go1.10.8",
	"go1.11.13",
	"go1.12.17",
	"go1.13.8",
	"go1.14",
}

const out = "dist"

type Config struct {
	Name       string
	GoVersion  string
	GOOS       string
	LinkMode   string
	StripDebug bool
	TrimPath   bool
	BuildTime  time.Time
}

func (c *Config) Cmd() *exec.Cmd {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		panic(err)
	}
	ldflags := fmt.Sprintf("-X 'main.info=%s' -linkmode=%s", b, c.LinkMode)
	if c.StripDebug {
		ldflags += " -s -w"
	}
	args := []string{
		"build",
		"-o", c.OutputPath(),
		"-ldflags", ldflags,
	}
	if c.TrimPath {
		args = append(args, "-trimpath")
	}
	args = append(args, "main.go")
	cmd := exec.Command(c.GoVersion, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("GOOS=%s", c.GOOS))
	return cmd
}

func (c *Config) OutputPath() string {
	name := fmt.Sprintf("%s-%s-%s-%slnk", c.Name, c.GoVersion, c.GOOS, c.LinkMode[:3])
	if c.StripDebug {
		name += "-strip"
	}
	if c.TrimPath {
		name += "-trimpath"
	}

	// Append a hash of the config to the file name such that whenever the
	// config changes we generate a new name, regardless of other parts of the
	// file name. We ignore the c.BuildTime, otherwise every build would have a
	// different hash. The intention is that rebuilding the same configuration
	// overwrites an old output binary.
	snapshot := *c
	snapshot.BuildTime = time.Time{}
	b, err := json.Marshal(snapshot)
	if err != nil {
		panic(err)
	}
	h := fnv.New32a()
	h.Write(b)
	name += "-" + hex.EncodeToString(h.Sum(nil))

	if c.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(out, name)
}

func goVersion(exe string) string {
	b, err := exec.Command(exe, "version").Output()
	if err != nil {
		panic(err)
	}
	version := string(bytes.Fields(b)[2])
	return version
}

func main() {
	buildTime := time.Now()
	name := "hello"
	hasUPX := exec.Command("upx", "-V").Run() == nil

	installMissingToolchains(versions)

	sem := make(chan struct{}, runtime.NumCPU())
	for _, exe := range versions {
		for _, GOOS := range []string{"linux", "darwin", "windows"} {
			for _, trimpath := range []bool{false, true} {
				for _, linkmode := range []string{"internal", "external"} {
					for _, strip := range []bool{false, true} {
						version := goVersion(exe)
						if version != exe {
							panic(fmt.Errorf("inconsistent go version: exe=%q, version=%q", exe, version))
						}

						v, err := strconv.Atoi(strings.Split(version, ".")[1])
						if err != nil {
							panic(err)
						}
						if trimpath && v < 13 {
							// -trimpath was added in go1.13
							continue
						}
						if linkmode == "external" && runtime.GOOS != GOOS {
							// cannot cross-compile using external linker
							continue
						}
						cfg := Config{
							Name:       name,
							GoVersion:  version,
							GOOS:       GOOS,
							LinkMode:   linkmode,
							StripDebug: strip,
							TrimPath:   trimpath,
							BuildTime:  buildTime,
						}
						sem <- struct{}{}
						go func() {
							fmt.Println(cfg.OutputPath())
							mustRunCmd(cfg.Cmd())
							if hasUPX {
								upx(cfg.OutputPath())
							}
							<-sem
						}()
					}
				}
			}
		}
	}
	for n := cap(sem); n > 0; n-- {
		sem <- struct{}{}
	}
}

// installMissingToolchains takes a list of Go versions (in go1.x[.x] format)
// and installs toolchains that are not available locally.
func installMissingToolchains(versions []string) {
	for _, version := range versions {
		installed := func() string {
			defer func() {
				recover()
			}()
			return goVersion(version)
		}()
		if installed != version {
			fmt.Println("installing", version)
			mustRun("go", "get", "golang.org/dl/"+version)
			mustRun(version, "download")
		}
	}
}

// upx compresses an executable with upx, leaving the original intact.
func upx(exe string) {
	out := strings.TrimSuffix(exe, ".exe")
	out += "-upx"
	if strings.HasSuffix(exe, ".exe") {
		out += ".exe"
	}
	fmt.Println(out)
	mustRun("upx", "-qq", "-f", "-o", out, exe)
}

// mustRun runs the command with the given name and arguments and panics if the
// execution failed.
func mustRun(name string, arg ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, arg...)
	mustRunCmd(cmd)
}

// mustRunCmd runs the cmd command and panics if the execution failed.
func mustRunCmd(cmd *exec.Cmd) {
	if b, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "$ %s\n%s\n^^^\n", cmd, b)
		panic(err)
	}
}
