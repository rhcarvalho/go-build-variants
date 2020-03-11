// +build ignore

package main

import (
	"bytes"
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

const out = "dist"

type Config struct {
	Name       string
	GoExe      string
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
	cmd := exec.Command(c.GoExe, args...)
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

	sem := make(chan struct{}, runtime.NumCPU())
	for _, exe := range []string{"go1.10", "go1.11", "go1.12", "go1.13", "go"} {
		for _, GOOS := range []string{"linux", "darwin", "windows"} {
			for _, trimpath := range []bool{false, true} {
				for _, linkmode := range []string{"internal", "external"} {
					for _, strip := range []bool{false, true} {
						version := goVersion(exe)
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
							GoExe:      exe,
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
							if b, err := cfg.Cmd().CombinedOutput(); err != nil {
								fmt.Printf("**Config**\n%+v\n**Output**\n%s\n", cfg, b)
								panic(err)
							}
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

// upx compresses an executable with upx, leaving the original intact.
func upx(exe string) {
	out := strings.TrimSuffix(exe, ".exe")
	out += "-upx"
	if strings.HasSuffix(exe, ".exe") {
		out += ".exe"
	}
	fmt.Println(out)
	cmd := exec.Command("upx", "-qq", "-f", "-o", out, exe)
	if b, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("**Input**\n%+v\n**Output**\n%s\n", exe, b)
		panic(err)
	}
}
