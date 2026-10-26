package main

import "testing"

func runPeep(t *testing.T, text string) []string {
	t.Helper()
	return splitLines(peephole(text))
}

// splitLines keeps the trailing empty piece out of the way of
// comparisons; peephole works on the whole text including it.
func splitLines(text string) []string {
	lines := splitAll(text)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func splitAll(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			out = append(out, text[start:i])
			start = i + 1
		}
	}
	return append(out, text[start:])
}

func TestPeepStoreLoadPair(t *testing.T) {
	got := runPeep(t, "    mov qword ptr [rbp-72], rax\n"+
		"    mov rax, qword ptr [rbp-72]\n"+
		"    mov rcx, rax\n")
	want := []string{
		"    mov qword ptr [rbp-72], rax",
		"    mov rcx, rax",
	}
	if len(got) != 2 {
		t.Fatalf("the reload survived: %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d wrong: %q", i, got[i])
		}
	}
}

func TestPeepStoreLoadDifferentSlots(t *testing.T) {
	got := runPeep(t, "    mov qword ptr [rbp-72], rax\n"+
		"    mov rax, qword ptr [rbp-80]\n")
	if len(got) != 2 {
		t.Fatalf("a load from another slot was dropped: %q", got)
	}
}

func TestPeepStoreLoadInterrupted(t *testing.T) {
	// Anything between the store and the reload can change rax, so the
	// pair only folds when nothing separates them.
	got := runPeep(t, "    mov qword ptr [rbp-72], rax\n"+
		"    call foo\n"+
		"    mov rax, qword ptr [rbp-72]\n")
	if len(got) != 3 {
		t.Fatalf("an interrupted pair folded: %q", got)
	}
}

func TestPeepDoubleReload(t *testing.T) {
	// Both reloads follow the store with nothing between them, so both
	// go and one store serves every reader.
	got := runPeep(t, "    mov qword ptr [rbp-8], rax\n"+
		"    mov rax, qword ptr [rbp-8]\n"+
		"    mov rax, qword ptr [rbp-8]\n")
	if len(got) != 1 {
		t.Fatalf("reloads survived: %q", got)
	}
}

func TestPeepNarrowImm(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"    mov rax, 42", "    mov eax, 42"},
		{"    mov rax, 0", "    mov eax, 0"},
		{"    mov rax, 2147483647", "    mov eax, 2147483647"},
		{"    mov rax, 2147483648", "    mov rax, 2147483648"},
		{"    mov rax, -7", "    mov rax, -7"},
		{"    mov rcx, 42", "    mov rcx, 42"},
		{"    mov rax, __str3[rip]", "    mov rax, __str3[rip]"},
	}
	for _, c := range cases {
		got := splitLines(peephole(c.in + "\n"))
		if len(got) != 1 || got[0] != c.out {
			t.Fatalf("%q became %q, want %q", c.in, got, c.out)
		}
	}
}

func TestPeepFloatRegMove(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"    movsd xmm4, xmm2", "    movapd xmm4, xmm2"},
		{"    movsd xmm10, xmm0", "    movapd xmm10, xmm0"},
		{"    movsd xmm0, qword ptr [r10+8]", "    movsd xmm0, qword ptr [r10+8]"},
		{"    movsd qword ptr [rsp+8], xmm1", "    movsd qword ptr [rsp+8], xmm1"},
		{"    movsd xmm0, __flt3[rip]", "    movsd xmm0, __flt3[rip]"},
	}
	for _, c := range cases {
		got := splitLines(peephole(c.in + "\n"))
		if len(got) != 1 || got[0] != c.out {
			t.Fatalf("%q became %q, want %q", c.in, got, c.out)
		}
	}
}

func TestPeepDuplicateLine(t *testing.T) {
	got := runPeep(t, "    lea rax, __str1[rip]\n"+
		"    lea rax, __str1[rip]\n"+
		"    mov rdx, rax\n")
	if len(got) != 2 {
		t.Fatalf("the duplicate survived: %q", got)
	}
}

func TestPeepDuplicateCallKept(t *testing.T) {
	// Two identical calls are two calls. Dropping one would halve a
	// program's side effects.
	got := runPeep(t, "    call foo\n"+
		"    call foo\n")
	if len(got) != 2 {
		t.Fatalf("a duplicated call lost its copy: %q", got)
	}
}

func TestPeepDuplicateAcrossLabelKept(t *testing.T) {
	// The label can be jumped to from anywhere, so the instruction
	// after it is not a repeat of whatever came before.
	got := runPeep(t, "    mov eax, 5\n"+
		".Lmain_0:\n"+
		"    mov eax, 5\n")
	if len(got) != 3 {
		t.Fatalf("a duplicate across a label folded: %q", got)
	}
}

func TestPeepFlagsUntouched(t *testing.T) {
	// A compare feeding a jump must pass through exactly as emitted.
	got := runPeep(t, "    cmp rax, 5\n"+
		"    jne .Lmain_7\n")
	if len(got) != 2 {
		t.Fatalf("a compare or jump was touched: %q", got)
	}
}

func TestPeepKeepsChainedLoads(t *testing.T) {
	// Two identical lines are not one line run twice when the
	// destination is also the address: the second loads through what
	// the first loaded. The register allocator produces this shape
	// once a result may take over its dying operand's register.
	text := "    mov r11, qword ptr [r11+0]\n" +
		"    mov r11, qword ptr [r11+0]\n"
	if got := runPeep(t, text); len(got) != 2 {
		t.Fatalf("a chained load was dropped: %q", got)
	}
	// A plain repeated move still goes.
	if got := runPeep(t, "    mov r8, r9\n    mov r8, r9\n"); len(got) != 1 {
		t.Fatalf("a repeated move was kept: %q", got)
	}
}
