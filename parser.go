// Package parser preserves the historical X analytics import.
// Publication is provided by the separate x-publish Python CLI.
package parser

import analytics "github.com/suenot/x-automation/analytics"

type Config = analytics.Config
type XParser = analytics.XParser

const DefaultUserAgent = analytics.DefaultUserAgent

func New(cfg Config) *XParser { return analytics.New(cfg) }
