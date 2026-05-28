package workers

import "embed"

//go:embed ansible
var ansibleAssets embed.FS

//go:embed cloud-agent/*
var cloudAgentAssets embed.FS

func cloudAgentAssetString(path string) string {
	payload, err := cloudAgentAssets.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(payload)
}
