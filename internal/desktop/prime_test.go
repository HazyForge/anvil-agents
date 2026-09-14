package desktop

import (
	"reflect"
	"testing"
)

func TestPrimeCatalogUsesNativeStdinJSON(t *testing.T) {
	tool, ok := catalogTool("prime")
	if !ok || !tool.Delegatable() || tool.Backend != "primeAgent" || tool.Invoke.Mode != PromptStdin {
		t.Fatalf("Prime catalog recipe = %#v", tool)
	}
	if !reflect.DeepEqual(tool.Binaries, []string{"prime-agent"}) || !reflect.DeepEqual(tool.Invoke.Args, []string{"--print", "--mode", "json", "--no-session"}) {
		t.Fatalf("Prime invocation = %#v %#v", tool.Binaries, tool.Invoke.Args)
	}
}
