/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"strings"

	slackapi "github.com/slack-go/slack"
)

type textBlockBuilder struct {
	elementType string
	text        string
	emoji       bool
	verbatim    bool
}

func mdText() *textBlockBuilder {
	return &textBlockBuilder{elementType: slackapi.MarkdownType}
}

func plainText() *textBlockBuilder {
	return &textBlockBuilder{elementType: slackapi.PlainTextType}
}

func (b *textBlockBuilder) WithText(text string) *textBlockBuilder {
	b.text = text
	return b
}

func (b *textBlockBuilder) WithEmoji() *textBlockBuilder {
	b.emoji = true
	return b
}

func (b *textBlockBuilder) WithoutEmoji() *textBlockBuilder {
	b.emoji = false
	return b
}

func (b *textBlockBuilder) WithVerbatim() *textBlockBuilder {
	b.verbatim = true
	return b
}

func (b *textBlockBuilder) Build() *slackapi.TextBlockObject {
	return slackapi.NewTextBlockObject(b.elementType, b.text, b.emoji, b.verbatim)
}

type blockSetBuilder struct {
	blocks []slackapi.Block
}

func newBlockSetBuilder(capacity int) *blockSetBuilder {
	if capacity < 0 {
		capacity = 0
	}
	return &blockSetBuilder{blocks: make([]slackapi.Block, 0, capacity)}
}

func (b *blockSetBuilder) Add(blocks ...slackapi.Block) *blockSetBuilder {
	b.blocks = append(b.blocks, blocks...)
	return b
}

func (b *blockSetBuilder) AddWhen(condition bool, block slackapi.Block) *blockSetBuilder {
	if !condition {
		return b
	}
	return b.Add(block)
}

func (b *blockSetBuilder) AddWhenText(text string, factory func(string) slackapi.Block) *blockSetBuilder {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return b
	}
	return b.Add(factory(trimmed))
}

func (b *blockSetBuilder) AddWhenTextBlocks(text string, factory func(string) []slackapi.Block) *blockSetBuilder {
	if strings.TrimSpace(text) == "" {
		return b
	}
	b.blocks = append(b.blocks, factory(text)...)
	return b
}

func (b *blockSetBuilder) Build() []slackapi.Block {
	return b.blocks
}
