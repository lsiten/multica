package service

import "testing"

func TestHumanTextChoiceMatchesWholeRevisionBoundAnswers(t *testing.T) {
	request := HumanRequestInput{Kind: "choice", ResponseMode: "chat_or_card", Choices: []HumanRequestChoice{{ID: "native", Label: "接受1086×1448原生尺寸"}, {ID: "resize", Label: "重新调整尺寸"}}}
	for _, text := range []string{"A", "a", "1", "选第1项", "选择1", "接受1086×1448原生尺寸", " A "} {
		answer, err := MatchHumanTextAnswer(request, 3, text)
		if err != nil || answer.Answer != "native" || answer.Revision != 3 {
			t.Fatalf("%q: %+v %v", text, answer, err)
		}
	}
	for _, text := range []string{"不是A", "不要选1", "A还是B？", "A，但修改尺寸", "5", "A/B", "他说选1", ""} {
		if answer, err := MatchHumanTextAnswer(request, 3, text); err == nil {
			t.Fatalf("discussion %q became a decision: %+v", text, answer)
		}
	}
	request.ResponseMode = ""
	if _, err := MatchHumanTextAnswer(request, 3, "A"); err == nil {
		t.Fatal("legacy request silently became text-enabled")
	}
}

func TestHumanTextChoiceDoesNotGuessAmbiguousLabels(t *testing.T) {
	request := HumanRequestInput{Kind: "choice", ResponseMode: "chat_or_card", Choices: []HumanRequestChoice{{ID: "first", Label: "same"}, {ID: "second", Label: "same"}}}
	if _, err := MatchHumanTextAnswer(request, 1, "same"); err == nil {
		t.Fatal("duplicate label was guessed")
	}
	request.Choices[1].Label = "A"
	if _, err := MatchHumanTextAnswer(request, 1, "A"); err == nil {
		t.Fatal("alias/label collision was guessed")
	}
}

func TestHumanTextOnlyInformationKindsMayEnableText(t *testing.T) {
	request := HumanRequestInput{Key: "address", Kind: "input", Title: "测试地址", ActionLabel: "提交", Next: "继续验证", InputLabel: "地址", ResponseMode: "chat_or_card"}
	answer, err := MatchHumanTextAnswer(request, 1, " https://test.example ")
	if err != nil || answer.Answer != " https://test.example " {
		t.Fatalf("input changed: %+v %v", answer, err)
	}
	request.Kind = "confirmation"
	if request.Validate() == nil {
		t.Fatal("authorization request accepted text mode")
	}
}
