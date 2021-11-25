module github.com/hnw/slack-commander

go 1.14

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/hashicorp/logutils v1.0.0
	github.com/mattn/go-shellwords v1.0.10
	github.com/slack-go/slack v0.10.0
)

replace github.com/hashicorp/logutils v1.0.0 => github.com/hnw/logutils v1.0.1-0.20211107152832-e280e29afdb3
