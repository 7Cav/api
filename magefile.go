//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Generate protobufs
func Generate() error {
	fmt.Println("Generating protobufs...")
	return run("buf", "generate")
}

// Lint and check for breaking changes
func Lint() error {
	fmt.Println("Running lint and breaking changes check...")
	if err := run("buf", "lint"); err != nil {
		return err
	}
	return run("buf", "breaking", "--against", "https://github.com/7cav/api.git#branch=develop")
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

// Install dependencies and tools
func Install() error {
	fmt.Println("Installing dependencies and tools...")
	if err := run("go", "install",
		"google.golang.org/protobuf/cmd/protoc-gen-go",
		"google.golang.org/grpc/cmd/protoc-gen-go-grpc",
		"github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway",
		"github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2",
	); err != nil {
		return err
	}
	return run("go", "get",
		"github.com/bufbuild/buf/cmd/buf",
		"github.com/square/certstrap",
		"github.com/spf13/cobra",
	)
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
