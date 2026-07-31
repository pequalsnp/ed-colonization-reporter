package config

import "testing"

func TestApplyEdcolonizeEnv(t *testing.T) {
	t.Run("env fills an empty config and enables the destination", func(t *testing.T) {
		t.Setenv(EnvEdcolonizeURL, "http://172.16.3.208:3000/api/cmdr/snapshot")
		t.Setenv(EnvEdcolonizeToken, "from-env")

		cfg := Default()
		applyEdcolonizeEnv(&cfg)

		if cfg.EdcolonizeURL != "http://172.16.3.208:3000/api/cmdr/snapshot" {
			t.Errorf("URL = %q", cfg.EdcolonizeURL)
		}
		if cfg.EdcolonizeToken != "from-env" {
			t.Errorf("token = %q", cfg.EdcolonizeToken)
		}
		// Pointing at a URL is an unambiguous request to use it — requiring a
		// second opt-in in a different place would be a papercut for a
		// headless setup.
		if !cfg.EdcolonizeEnabled {
			t.Error("setting the URL via env should enable the destination")
		}
	})

	t.Run("env overrides the config file", func(t *testing.T) {
		t.Setenv(EnvEdcolonizeURL, "http://env-wins:3000/api/cmdr/snapshot")
		t.Setenv(EnvEdcolonizeToken, "env-token")

		cfg := Config{
			EdcolonizeEnabled: true,
			EdcolonizeURL:     "http://from-file:3000/api/cmdr/snapshot",
			EdcolonizeToken:   "file-token",
		}
		applyEdcolonizeEnv(&cfg)

		if cfg.EdcolonizeURL != "http://env-wins:3000/api/cmdr/snapshot" {
			t.Errorf("URL = %q, want the env value", cfg.EdcolonizeURL)
		}
		if cfg.EdcolonizeToken != "env-token" {
			t.Errorf("token = %q, want the env value", cfg.EdcolonizeToken)
		}
	})

	t.Run("unset env leaves the config file alone", func(t *testing.T) {
		t.Setenv(EnvEdcolonizeURL, "")
		t.Setenv(EnvEdcolonizeToken, "")

		cfg := Config{
			EdcolonizeEnabled: true,
			EdcolonizeURL:     "http://from-file:3000/api/cmdr/snapshot",
			EdcolonizeToken:   "file-token",
		}
		applyEdcolonizeEnv(&cfg)

		if cfg.EdcolonizeURL != "http://from-file:3000/api/cmdr/snapshot" {
			t.Errorf("URL = %q, want the file value preserved", cfg.EdcolonizeURL)
		}
		if cfg.EdcolonizeToken != "file-token" {
			t.Errorf("token = %q, want the file value preserved", cfg.EdcolonizeToken)
		}
	})

	t.Run("whitespace-only env is treated as unset", func(t *testing.T) {
		t.Setenv(EnvEdcolonizeURL, "   ")

		cfg := Config{EdcolonizeURL: "http://from-file:3000/api/cmdr/snapshot"}
		applyEdcolonizeEnv(&cfg)

		if cfg.EdcolonizeURL != "http://from-file:3000/api/cmdr/snapshot" {
			t.Errorf("URL = %q, want the file value preserved", cfg.EdcolonizeURL)
		}
		if cfg.EdcolonizeEnabled {
			t.Error("blank env must not enable the destination")
		}
	})

	t.Run("token alone does not enable the destination", func(t *testing.T) {
		t.Setenv(EnvEdcolonizeToken, "orphan-token")

		cfg := Default()
		applyEdcolonizeEnv(&cfg)

		if cfg.EdcolonizeEnabled {
			t.Error("a token with no URL must not enable the destination")
		}
	})
}

// The edcolonize settings must round-trip through TOML like every other
// destination's, so the Settings GUI can persist them.
func TestEdcolonizeSettingsRoundTripTOML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.toml"

	in := Default()
	in.EdcolonizeEnabled = true
	in.EdcolonizeURL = "http://172.16.3.208:3000/api/cmdr/snapshot"
	in.EdcolonizeToken = "persisted"

	if err := SaveTo(in, path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	out, existed, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !existed {
		t.Fatal("LoadFrom reported the file as missing")
	}
	if out.EdcolonizeEnabled != true ||
		out.EdcolonizeURL != in.EdcolonizeURL ||
		out.EdcolonizeToken != in.EdcolonizeToken {
		t.Errorf("round trip lost edcolonize settings: %+v", out)
	}
}
