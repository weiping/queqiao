package main

import "fmt"

// serviceCmd and migrateCmd arrive with SP8 Tasks 11 and 12.
func serviceCmd(args []string) error { return fmt.Errorf("queqiao service: not yet") }
func migrateCmd(args []string) error { return fmt.Errorf("queqiao migrate: not yet") }
