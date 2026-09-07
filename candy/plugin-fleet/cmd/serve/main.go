// Command serve is the OUT-OF-PROCESS entrypoint for the deploy command plugin: dual-mode
// sdk.Main (serve OR CLI). charly fork/execs this binary in CLI mode for command:deploy
// dispatch when the plugin is NOT compiled-in (→ CliMain); the serve half backs the
// out-of-process provider placement. The SAME NewProvider()/NewMeta() compile INTO
// charly in-process when listed in compiled_plugins — placement is invisible (F8).
package main

import (
	deploy "github.com/opencharly/plugin-fleet/candy/plugin-fleet"
	"github.com/opencharly/sdk"
)

func main() { sdk.Main(deploy.NewProvider(), deploy.NewMeta(), deploy.CliMain) }
