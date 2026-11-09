package preview

import (
	"encoding/json"
	"io"
	"os"

	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
)

// not-wire-format: private process-edge test receipt, never gateway/SPA data.
type q62TargetReceipt struct {
	Stage   string
	Program string
	PID     int
	Env     []string
}

func q62TargetSnapshot(program string) q62TargetReceipt {
	return q62TargetReceipt{Stage: "final-target", Program: program, PID: os.Getpid(), Env: os.Environ()}
}

// Existing test hosts dispatch run-limited on every supported platform. These
// unique target modes run only AFTER that real launcher has executed the target.
func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "__q62-env-dump":
		if json.NewEncoder(os.Stdout).Encode(q62TargetSnapshot("dump")) != nil {
			os.Exit(71)
		}
		os.Exit(0)
	case "__q62-protocol-env":
		os.Exit(q62ProtocolTarget())
	}
}

func q62ProtocolTarget() int {
	if len(os.Args) != 3 {
		return 72
	}
	_, size, err := protocol.ReadRequest(os.Stdin)
	if err != nil {
		return 73
	}
	if _, err = io.CopyN(io.Discard, os.Stdin, size); err != nil {
		return 74
	}
	data, err := json.Marshal(q62TargetSnapshot(os.Args[2]))
	if err != nil {
		return 75
	}
	writer := protocol.NewFrameWriter(os.Stdout)
	if writer.Meta(protocol.Meta{ProtocolVersion: 1, BuildID: os.Args[2], PageCount: 1}) != nil {
		return 76
	}
	if _, err = writer.Text(1, string(data)); err != nil {
		return 77
	}
	if writer.Done() != nil {
		return 78
	}
	return 0
}
