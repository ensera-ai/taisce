// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

// `taisce conformance` runs the adapter conformance suite against a live deployment, with the
// adapter reached as a driver over the subprocess protocol (internal/conformance). It is in this
// binary so that an adapter repository, which is a different repository in a different language,
// runs the suite with the same runner every other adapter runs, from a released binary,
// and never with a copy of it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ensera-ai/taisce/internal/conformance"
)

func conformanceCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("conformance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	api := flags.String("api", os.Getenv(envAPI), "base URL of the deployment (or "+envAPI+")")
	cases := flags.String("cases", "conformance/cases.json", "the suite file")
	driver := flags.String("driver", "", "the adapter's driver command, speaking the subprocess protocol on stdin and stdout")
	reference := flags.Bool("reference", false, "run the reference adapter instead of a driver, to prove the deployment and the suite")
	serve := flags.Bool("serve-reference", false, "serve the reference adapter over stdin and stdout as a driver would")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("conformance: %w", err)
	}
	if *serve {
		return conformance.Serve(ctx, os.Stdin, out, &conformance.Reference{})
	}
	if *api == "" {
		return errors.New("conformance: the deployment address is required (--api or " + envAPI + ")")
	}
	token := strings.TrimSpace(os.Getenv(envToken))
	if token == "" {
		return errors.New("conformance: " + envToken + " must hold a write-enabled credential; it is never taken from a flag")
	}
	suite, err := conformance.Load(*cases)
	if err != nil {
		return fmt.Errorf("conformance: %w", err)
	}
	var d conformance.Driver
	switch {
	case *reference && *driver != "":
		return errors.New("conformance: name a driver or the reference, not both")
	case *reference:
		d = &conformance.Reference{}
	case *driver != "":
		sub := &conformance.Subprocess{Command: strings.Fields(*driver), Stderr: os.Stderr}
		defer sub.Close()
		d = sub
	default:
		return errors.New("conformance: name the adapter's driver (--driver) or --reference")
	}
	results, err := conformance.Run(ctx, conformance.Deployment{API: *api, Token: token}, d, suite)
	if err != nil {
		return fmt.Errorf("conformance: %w", err)
	}
	failed := 0
	for _, r := range results {
		if !r.Passed {
			failed++
		}
		if err := json.NewEncoder(out).Encode(r); err != nil {
			return err
		}
	}
	if err := json.NewEncoder(out).Encode(struct {
		Suite  string `json:"suite"`
		Cases  int    `json:"cases"`
		Failed int    `json:"failed"`
	}{suite.Suite, len(results), failed}); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("conformance: %d of %d cases failed", failed, len(results))
	}
	return nil
}
