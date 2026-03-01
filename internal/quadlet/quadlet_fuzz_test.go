package quadlet

import "testing"

func FuzzUnitNameFromQuadlet(f *testing.F) {
	f.Add("myapp.container")
	f.Add("myvolume.volume")
	f.Add("mynet.network")
	f.Add("app.kube")
	f.Add("base.image")
	f.Add("ci.build")
	f.Add("group.pod")
	f.Add("")
	f.Add(".container")
	f.Add("no-extension")
	f.Add("/deep/nested/path/app.container")
	f.Add("a\x00b.container")
	f.Add("name.unknown")

	f.Fuzz(func(_ *testing.T, path string) {
		// Should never panic regardless of input.
		_ = UnitNameFromQuadlet(path)
	})
}

func FuzzIsQuadletFile(f *testing.F) {
	f.Add("myapp.container")
	f.Add("readme.txt")
	f.Add("")
	f.Add(".")
	f.Add("no-ext")

	f.Fuzz(func(_ *testing.T, path string) {
		// Should never panic regardless of input.
		_ = IsQuadletFile(path)
	})
}
