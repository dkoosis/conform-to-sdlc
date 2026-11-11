module github.com/dkoosis/repo-standard

go 1.26.3

toolchain go1.26.5

require github.com/quasilyte/go-ruleguard/dsl v0.3.23

require gopkg.in/yaml.v3 v3.0.1 // indirect

// v0.1.0 and v0.1.1 were tagged at the cfm-1e1.3 (Surface 2) commit, before
// Surface 3 merged — a module fetched at either tag is missing --fleet.
// v0.1.2 is the first release with all three surfaces.
retract (
	v0.1.0 // mis-tagged before Surface 3 (--fleet) merged
	v0.1.1 // mis-tagged before Surface 3 (--fleet) merged
)
