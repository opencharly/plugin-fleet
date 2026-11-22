package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"google.golang.org/grpc"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// builderStubClient answers the builder plugin's two deploy-time legs (OpCollectContext, OpReverse)
// the way a real builder plugin does, and records which builder words were dispatched. Every other
// executor method is the shared nopSeamExecutorClient's (unreachable here).
type builderStubClient struct {
	nopSeamExecutorClient
	dispatched []string
}

func (c *builderStubClient) InvokeProvider(_ context.Context, in *pb.InvokeProviderRequest, _ ...grpc.CallOption) (*pb.InvokeReply, error) {
	if in.GetClass() != "builder" {
		return nil, fmt.Errorf("builderStubClient: unexpected class %q", in.GetClass())
	}
	c.dispatched = append(c.dispatched, in.GetReserved()+":"+in.GetOp())
	var out any
	switch in.GetOp() {
	case sdk.OpCollectContext:
		var req spec.BuilderCollectInput
		if err := json.Unmarshal(in.GetParamsJson(), &req); err != nil {
			return nil, err
		}
		out = spec.BuilderCollectReply{
			Context: map[string]any{"packages": []string{"kdotool"}},
			Package: &spec.BuilderPackage{Format: "pac", Dir: "/tmp/aur-pkgs/" + req.Candy, Glob: "*.pkg.tar.zst"},
		}
	case sdk.OpReverse:
		out = spec.BuilderReverseReply{}
	default:
		return nil, fmt.Errorf("builderStubClient: unexpected op %q", in.GetOp())
	}
	j, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// The deploy pre-pass carries the package output the builder PLUGIN declares (its collect reply)
// onto the pre-resolved entry the apply compile reads (opencharly/sdk#608): on an arch image an
// arch-tagged aur candy's entry holds that package, so the compile installs it through local_pkg.
// On a fedora image the same candy's arch-tagged declaration is out of scope, so the pre-pass
// dispatches no builder at all. A candy in the same order that does not need the builder is never
// dispatched.
func TestPreresolveCarriesThePluginsDeclaredPackageOutput(t *testing.T) {
	candies := map[string]spec.CandyReader{
		"kde-shell": aurCandy("kde-shell", "kdotool"),
		"plain":     testCandy("plain", spec.CandyModel{}, spec.CandyView{}),
	}
	order := []string{"kde-shell", "plain"}

	stub := &builderStubClient{}
	got, err := preresolveBuilderContexts(context.Background(), sdk.NewInProcExecutor(stub), order, candies, builderTestImg("arch"))
	if err != nil {
		t.Fatalf("preresolve on arch: %v", err)
	}
	entry, ok := got[deploykit.BuilderCtxKey("kde-shell", "aur")]
	if !ok {
		t.Fatalf("no pre-resolved aur entry for kde-shell on arch; got keys %v (dispatched %v)", keysOf(got), stub.dispatched)
	}
	// Only the candy that needs the builder is pre-resolved: "plain" declares nothing aur-related.
	if len(got) != 1 {
		t.Errorf("pre-resolved %v, want only kde-shell's aur entry (a candy that does not need the builder must not be dispatched)", keysOf(got))
	}
	want := spec.BuilderPackage{Format: "pac", Dir: "/tmp/aur-pkgs/kde-shell", Glob: "*.pkg.tar.zst"}
	if entry.Package == nil || *entry.Package != want {
		t.Errorf("pre-resolved aur package = %+v, want %+v (the plugin's declared output must reach the compile)", entry.Package, want)
	}

	fedora := &builderStubClient{}
	got, err = preresolveBuilderContexts(context.Background(), sdk.NewInProcExecutor(fedora), order, candies, builderTestImg("fedora"))
	if err != nil {
		t.Fatalf("preresolve on fedora: %v", err)
	}
	if len(got) != 0 || len(fedora.dispatched) != 0 {
		t.Errorf("fedora: pre-resolved %v and dispatched %v, want none (an arch-tagged aur declaration is out of scope)", keysOf(got), fedora.dispatched)
	}
}

func keysOf(m map[string]deploykit.BuilderPreresolved) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
