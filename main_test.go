package main

import "testing"

func init() {
	EndPoint = "http://127.0.0.1:5001"
	Verbose = true
}

func TestListKeys(t *testing.T) {
	keys, err := ListKeys()
	if err != nil {
		t.Error(err)
	}
	if keys == nil {
		t.Error("Keys are nil!")
	}
}

func TestResolveIPNS(t *testing.T) {
	cid, err := ResolveIPNS("k51qzi5uqu5djwygzxb01sprni3r6u2nru36gxabe5w8n3go27hxc819ic2w1q")
	if err != nil {
		t.Error(err)
	}
	if cid != "QmWa7egj1g4Dmv35s1AauW4KZqMBA84WqrRduxRYbQ5T3p" {
		t.Error("Unexpected CID returned from IPNS query.")
	}
}

func TestCleanFilestore(t *testing.T) {
	CleanFilestore()
}