package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/newalchemylimited/seth"
)

var cmdjumptab = &cmd{
	desc: "print the jump table of a contract",
	do:   jumptab,
}

// just the opcodes we need to parse the jump table
const (
	opdup1  = 0x80
	oppush1 = 0x60
	opeq    = 0x14
	opjumpi = 0x57
)

type jmpentry struct {
	prefix  [4]byte // jump table prefix
	jmpdest int     // PC of actual code
}

// preimage is a list of common function selectors
var preimage = []string{
	"balanceOf(address)",
	"totalSupply()",
	"transfer(address,uint256)",
	"transferFrom(address,address,uint256)",
	"approve(address,uint256)",
	"changeOwner(address)",
	"acceptOwnership()",
	"mint(address,uint256)",
	"pause()",
	"finalize()",
	"name()",
	"decimals()",
	"owner()",
	"finalized()",
	"allowance(address,address)",
	"locked()",
	"burn(address,uint256)",
}

// code sequences that are equivalent to
//   (calldata[0] >> 224) & 0xffffffff
var prefixes = []string{
	// For some reason, _later_ versions of solc
	// produce this code, despite the size regression...
	//
	// PUSH1 0x0 CALLDATALOAD PUSH29
	// 0x100000000000000000000000000000000000000000000000000000000
	// SWAP1 DIV PUSH4 0xFFFFFFFF AND
	"7c0100000000000000000000000000000000000000000000000000000000900463ffffffff16",

	// PUSH4 0xffffffff
	// PUSH1 0xe0 PUSH1 0x02 EXP
	// PUSH1 0x00 CALLDATALOAD
	// DIV AND
	"63ffffffff60e060020a6000350416",
}

type jmpformat struct {
	prefix, suffix string
}

func jumptab(args []string) {
	if len(args) != 1 {
		fatalf("usage: eth jumptab <address|->\n")
	}
	var code []byte
	if args[0] == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			fatalf("couldn't read stdin: %s\n", err)
		}
		if len(buf) > 1 && buf[len(buf)-1] == '\n' {
			buf = buf[:len(buf)-1]
		}
		code = make([]byte, hex.DecodedLen(len(buf)))
		_, err = hex.Decode(code, buf)
		if err != nil {
			fatalf("couldn't decode input: %s\n", err)
		}
	} else {
		var addr seth.Address
		err := addr.FromString(args[0])
		if err != nil {
			fatalf("jumptab: bad address: %s\n", err)
		}
		code = getcode(client(), &addr)
	}
	if len(code) == 0 {
		fatalf("address has no code\n")
	}

	// for each of the possible jump table preambles,
	// try to find an appropriate match in the code
	preamble := -1
	for _, p := range prefixes {
		buf, err := hex.DecodeString(p)
		if err != nil {
			panic(err)
		}
		preamble = bytes.Index(code, buf)
		if preamble != -1 {
			preamble = preamble + len(buf)
			break
		}
	}
	if preamble == -1 {
		fatalf("couldn't find a jump table preamble\n")
	}

	// supported jump table formats:
	//
	//   DUP1 PUSH4 0x06fdde03 EQ PUSH2 0x0145 JUMPI
	//
	//   PUSH4 0x06fdde03 DUP2 EQ PUSH2 0x0145 JUMPI
	//
	// TODO: is the PUSH after EQ always PUSH2?
	// That would make the code a bit simpler.
	var entries []jmpentry
	base := code[preamble:]
	for len(base) > 12 {
		var pushbytes, prefixbytes [4]byte

		if base[0] == oppush1+3 &&
			base[5] == opdup1+1 &&
			base[6] == opeq {
			// first case: PUSH4 <prefix> DUP2 EQ
			copy(prefixbytes[:], base[1:4])
		} else if base[0] == opdup1 &&
			base[1] == oppush1+3 &&
			base[6] == opeq {
			// second case: DUP1 PUSH4 <prefix> EQ
			copy(prefixbytes[:], base[2:6])
		} else {
			break
		}

		// width of PUSH used to identify PC
		pwidth := 1 + int(base[7]-oppush1)
		if pwidth > 4 {
			break // ???
		}
		copy(pushbytes[:], base[8:8+pwidth])
		if base[8+pwidth] != opjumpi {
			break // ???
		}
		entries = append(entries, jmpentry{
			prefix:  prefixbytes,
			jmpdest: int(binary.BigEndian.Uint32(pushbytes[:])),
		})
		base = base[8+pwidth+1:]
	}

	if len(entries) == 0 {
		return
	}

	dict := make(map[uint32]string)
	for _, sig := range preimage {
		h := seth.HashString(sig)
		dict[binary.LittleEndian.Uint32(h[:4])] = sig
	}
	for i := range entries {
		sigword := binary.LittleEndian.Uint32(entries[i].prefix[:])
		sig := dict[sigword]
		if sig == "" {
			fmt.Printf("%x pc:%10d\n", entries[i].prefix[:], entries[i].jmpdest)
		} else {
			fmt.Printf("%x pc:%10d %s\n", entries[i].prefix[:], entries[i].jmpdest, sig)
		}
	}
}
