package providers

import "testing"

func TestOpenCodeZenBigPickleTestingIdentities(t *testing.T) {
	profile, err := GenerationProfileByName("opencode-zen-big-pickle")
	if err != nil {
		t.Fatalf("generation profile: %v", err)
	}
	if profile.Provider != "opencode-zen" || profile.Model != "big-pickle" {
		t.Fatalf("profile = %+v, want opencode-zen/big-pickle", profile)
	}
	rubric, err := RubricByVersion("rubric-v1-big-pickle")
	if err != nil {
		t.Fatalf("rubric: %v", err)
	}
	if rubric.JudgeProvider != "opencode-zen" || rubric.JudgeModel != "big-pickle" {
		t.Fatalf("rubric judge = %s/%s, want opencode-zen/big-pickle", rubric.JudgeProvider, rubric.JudgeModel)
	}
	rate, ok := RateFor("opencode-zen", "big-pickle")
	if !ok || rate.InputPerMillion != 0 || rate.OutputPerMillion != 0 {
		t.Fatalf("rate = %+v, found=%v, want explicit zero-rate free model", rate, ok)
	}
}

func TestOpenCodeZenMimoFreeTestingIdentities(t *testing.T) {
	profile, err := GenerationProfileByName("opencode-zen-mimo-v2.5-free")
	if err != nil {
		t.Fatalf("generation profile: %v", err)
	}
	if profile.Provider != "opencode-zen" || profile.Model != "mimo-v2.5-free" {
		t.Fatalf("profile = %+v, want opencode-zen/mimo-v2.5-free", profile)
	}
	rubric, err := RubricByVersion("rubric-v1-mimo-free")
	if err != nil {
		t.Fatalf("rubric: %v", err)
	}
	if rubric.JudgeProvider != "opencode-zen" || rubric.JudgeModel != "mimo-v2.5-free" {
		t.Fatalf("rubric judge = %s/%s, want opencode-zen/mimo-v2.5-free", rubric.JudgeProvider, rubric.JudgeModel)
	}
	rate, ok := RateFor("opencode-zen", "mimo-v2.5-free")
	if !ok || rate.InputPerMillion != 0 || rate.OutputPerMillion != 0 {
		t.Fatalf("rate = %+v, found=%v, want explicit zero-rate free model", rate, ok)
	}
}

func TestHuggingFaceGemmaTestingIdentities(t *testing.T) {
	profile, err := GenerationProfileByName("huggingface-gemma-3-4b-it-free")
	if err != nil {
		t.Fatalf("generation profile: %v", err)
	}
	if profile.Provider != "huggingface-chat" || profile.Model != "google/gemma-3-4b-it:featherless-ai" {
		t.Fatalf("profile = %+v, want huggingface-chat/gemma provider route", profile)
	}
	rubric, err := RubricByVersion("rubric-v1-huggingface-gemma")
	if err != nil {
		t.Fatalf("rubric: %v", err)
	}
	if rubric.JudgeProvider != "huggingface-chat" || rubric.JudgeModel != profile.Model {
		t.Fatalf("rubric judge = %s/%s, want %s/%s", rubric.JudgeProvider, rubric.JudgeModel, profile.Provider, profile.Model)
	}
}
