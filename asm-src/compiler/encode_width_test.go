package main

import (
	"strings"
	"testing"
)

// TestEncoderWidths holds the instructions behind mem.read and mem.write
// and extern struct fields to GNU as, one line at a time. No example
// program is guaranteed to use every width, so they are listed here.
func TestEncoderWidths(t *testing.T) {
	as, _, binDir := findToolchainForTest(t)
	lines := []string{
		"movzx eax, byte ptr [rax]",
		"movzx eax, word ptr [rax]",
		"movzx eax, word ptr [r9]",
		"movsx rax, byte ptr [rax]",
		"movsx rax, word ptr [rax]",
		"movsx rax, word ptr [rcx]",
		"movsx rax, byte ptr [r8]",
		"movsxd rax, dword ptr [rax]",
		"mov eax, dword ptr [rax]",
		"mov byte ptr [rax], cl",
		"mov word ptr [rax], cx",
		"mov word ptr [r8], r9w",
		"mov dword ptr [rax], ecx",
		"mov dword ptr [rax], r10d",
		"mov qword ptr [rax], rcx",
		"cvtss2sd xmm0, dword ptr [rax]",
		"cvtss2sd xmm1, dword ptr [r11]",
		"cvtsd2ss xmm0, xmm0",
		"cvtsd2ss xmm1, xmm0",
		"movss dword ptr [rax], xmm0",
		"movss xmm0, dword ptr [rax]",
		"movss dword ptr [r10], xmm1",
		// callback stubs
		"movsxd rcx, ecx",
		"movsxd r9, r9d",
		"test edx, edx",
		"setne dl",
		"movzx edx, dl",
		"movzx r8d, r8b",
		"movsxd rax, dword ptr [rsp+40]",
		"mov qword ptr [rsp+48], rax",
		"mov eax, dword ptr [rsp+56]",
		// atomics, and the register swap the argument scheduler uses
		"lock xadd qword ptr [rcx], rax",
		"lock xadd qword ptr [r10], r11",
		"lock cmpxchg qword ptr [rcx], rdx",
		"lock cmpxchg qword ptr [r8+16], r9",
		"xchg qword ptr [rcx], rax",
		"xchg r8, r9",
		"sete al",
		"movzx eax, al",
		// fixed-width numbers
		"movsx rax, al",
		"movsx rax, ax",
		"movzx eax, ax",
		"movsxd rax, eax",
		"mov eax, eax",
		"div rcx",
		"div r9",
		"shr rax, cl",
		"shr rcx, 1",
		"and eax, 1",
		"or rcx, rax",
		"cvtsi2ss xmm0, rax",
		"cvtsi2ss xmm0, rcx",
		"cvtsi2sd xmm0, rcx",
		"cvtss2sd xmm0, xmm0",
		"addsd xmm0, xmm0",
		"subsd xmm0, xmm1",
		"comisd xmm0, xmm1",
		"cvttsd2si rax, xmm0",
		"shl rcx, 63",
		"xor rax, rcx",
		"setb dl",
		"setbe dl",
		"seta dl",
		"setae dl",
		"cmp dword ptr [rbp-32], 0",
		"mov dword ptr [rbp-32], r10d",
		"mov r10d, 1",
		"xor r10d, r10d",
		// the wide packed loops and __vy_cpufeat; see vecloop.go
		"vmovupd ymm0, [r12+rbx*8]",
		"vmovupd ymm1, [r10+32]",
		"vmovupd [r10], ymm0",
		"vmovupd [rax+r9*8+32], ymm1",
		"vmovupd ymm0, [rbx]",
		"movupd xmm1, [r13+rcx*8+16]",
		"movupd [rax+rcx*8+16], xmm0",
		"vmovupd ymm0, [rbp+rcx*8]",
		"vmovupd ymm0, [rax+rcx*8+1024]",
		"vaddpd ymm0, ymm0, ymm1",
		"vsubpd ymm1, ymm1, ymm0",
		"vmulpd ymm0, ymm0, ymm5",
		"vbroadcastsd ymm5, xmm7",
		"vbroadcastsd ymm5, xmm12",
		"vbroadcastsd ymm5, qword ptr [rbp-24]",
		"vbroadcastsd ymm5, qword ptr [r13+8]",
		"vzeroupper",
		// SCANDIACUS_FMA; see fma.go
		"vfmadd231sd xmm0, xmm1, xmm7",
		"vfmadd231sd xmm0, xmm12, qword ptr [rbp-40]",
		"vfnmadd231sd xmm0, xmm3, xmm9",
		"vfmadd231sd xmm0, xmm2, qword ptr [r10+8]",
		"cpuid",
		"xgetbv",
		"mov eax, ecx",
		"and eax, 402653184",
		"cmp eax, 402653184",
		"and ebx, 32",
		"xor ecx, ecx",
		"mov qword ptr [rcx+1000], r8",
		// fixed-width callback arguments
		"movsx rcx, cl",
		"movsx r8, r8b",
		"movsx rdx, dx",
		"movsx r9, r9w",
		"movzx ecx, cx",
		"movzx r9d, r9w",
		"movzx edx, dl",
		"mov ecx, ecx",
		"mov r9d, r9d",
		"cvtss2sd xmm1, xmm1",
		"cvtss2sd xmm3, xmm3",
		"cvtss2sd xmm5, dword ptr [rsp+40]",
		"movsd qword ptr [rsp+40], xmm5",
		"movsx rax, byte ptr [rsp+40]",
		"movzx eax, word ptr [rsp+48]",
		"sub rsp, 40",
		"add rsp, 40",
		// fixed-width extension in place
		"movsx r8, r9b",
		"movsx rbx, sil",
		"movsx r15, r14b",
		"movsx r8, r9w",
		"movsx rsi, word ptr [rbp-16]",
		"movzx r8d, r9b",
		"movzx esi, dil",
		"movzx edi, sil",
		"movzx r8d, r9w",
		"movzx r8d, byte ptr [rbp-8]",
		"movsxd r8, r9d",
		"movsxd rbx, esi",
		"movsxd r12, dword ptr [rbp-24]",
		"mov r8d, r10d",
		"mov ebx, esi",
		"mov r10d, r10d",
		"mov r13d, dword ptr [rbp-8]",
	}
	for _, line := range lines {
		text := ".intel_syntax noprefix\n.text\n" + line + "\n"
		want, _ := assembleText(t, as, binDir, text)
		got := encodeText(t, text)
		if len(got) > len(want) || string(want[:len(got)]) != string(got) {
			t.Errorf("%s: encoded % x, the assembler says % x", line, got, want)
			continue
		}
		if rest := want[len(got):]; strings.Trim(string(rest), "\x00\x90") != "" {
			t.Errorf("%s: encoded % x, the assembler says % x", line, got, want)
		}
	}
}
