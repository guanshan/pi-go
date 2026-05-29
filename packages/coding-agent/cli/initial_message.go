package cli

import "github.com/guanshan/pi-go/packages/ai"

type InitialMessageInput struct {
	Parsed       Args
	FileText     string
	FileImages   []ai.ContentBlock
	StdinContent string
}

func BuildInitialMessage(input InitialMessageInput) (string, []ai.ContentBlock) {
	text, images, _ := BuildInitialMessageWithRemaining(input)
	return text, images
}

func BuildInitialMessageWithRemaining(input InitialMessageInput) (string, []ai.ContentBlock, []string) {
	var text string
	if input.StdinContent != "" {
		text += input.StdinContent
	}
	if input.FileText != "" {
		text += input.FileText
	}
	if len(input.Parsed.Messages) > 0 {
		text += input.Parsed.Messages[0]
	}
	remaining := []string(nil)
	if len(input.Parsed.Messages) > 1 {
		remaining = append([]string(nil), input.Parsed.Messages[1:]...)
	}
	if text == "" && len(input.FileImages) == 0 {
		return "", nil, remaining
	}
	return text, input.FileImages, remaining
}
