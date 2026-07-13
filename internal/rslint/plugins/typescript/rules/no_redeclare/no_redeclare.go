package no_redeclare

import (
	"github.com/i2y/ramune/internal/rslint/rule"
	coreNoRedeclare "github.com/i2y/ramune/internal/rslint/rules/no_redeclare"
)

var NoRedeclareRule = rule.CreateRule(rule.Rule{
	Name: "no-redeclare",
	Run:  coreNoRedeclare.RunTSESLint,
})
