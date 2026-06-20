//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Lint vets the code. The proto/buf codegen toolchain (and its `buf lint` /
// `buf breaking` checks) was retired in Phase 4 (#135); the API is plain
// net/http with hand-written handlers, so `go vet` is the lint gate.
func Lint() error {
	fmt.Println("Running go vet...")
	return run("go", "vet", "./...")
}

// Run the test suite (MariaDB integration tests skip unless TESTDB_ADDR is set)
func Test() error {
	fmt.Println("Running tests...")
	return run("go", "test", "./...")
}

// Spin up the dockerized MariaDB harness and run the full test suite against it.
// The compose project is unique per checkout and the host port ephemeral
// (discovered via `docker compose port`), so parallel worktrees can run
// harnesses side by side. Mirrors the Makefile's testdb targets.
func TestIntegration() error {
	fmt.Println("Running integration tests against the MariaDB harness...")
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	compose := []string{"compose", "-p", "testdb-" + strings.ToLower(filepath.Base(wd)), "-f", "testdb/compose.yaml"}
	if err := run("docker", append(compose, "up", "-d", "--wait")...); err != nil {
		return err
	}
	out, err := exec.Command("docker", append(compose, "port", "mariadb", "3306")...).Output()
	if err != nil {
		return fmt.Errorf("discovering harness port: %w", err)
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Env = append(os.Environ(), "TESTDB_ADDR="+strings.TrimSpace(string(out)))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("integration tests failed: %w", err)
	}
	return nil
}

// Helper function to run commands
func run(name string, args ...string) error {
	fmt.Printf("Running command: %s %v\n", name, args)
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to run %s %v: %w", name, args, err)
	}
	return nil
}
