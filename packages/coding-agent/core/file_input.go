package core

import (
	"github.com/guanshan/pi-go/packages/ai"
	"github.com/guanshan/pi-go/packages/coding-agent/cli"
)

type PreparedInitialMessage struct {
	Message   string
	Images    []ai.ContentBlock
	Remaining []string
}

func PrepareInitialMessage(cwd string, args cli.Args, stdinContent string, autoResizeImages bool) (string, []ai.ContentBlock, error) {
	prepared, err := PrepareInitialPrompt(cwd, args, stdinContent, autoResizeImages)
	if err != nil {
		return "", nil, err
	}
	return prepared.Message, prepared.Images, nil
}

func PrepareInitialPrompt(cwd string, args cli.Args, stdinContent string, autoResizeImages bool) (PreparedInitialMessage, error) {
	processed, err := cli.ProcessFileArguments(cwd, args.FileArgs, cli.ProcessFileOptions{AutoResizeImages: autoResizeImages})
	if err != nil {
		return PreparedInitialMessage{}, err
	}
	text, images, remaining := cli.BuildInitialMessageWithRemaining(cli.InitialMessageInput{
		Parsed:       args,
		FileText:     processed.Text,
		FileImages:   processed.Images,
		StdinContent: stdinContent,
	})
	return PreparedInitialMessage{Message: text, Images: images, Remaining: remaining}, nil
}
