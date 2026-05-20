//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
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
