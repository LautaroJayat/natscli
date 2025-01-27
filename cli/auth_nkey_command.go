package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/choria-io/fisk"
	"github.com/nats-io/nkeys"
)

type authNKCommand struct {
	keyType       string
	pubOut        bool
	entropySource string
	outFile       string
	keyFile       string
	dataFile      string
	signFile      string
}

func configureAuthNkeyCommand(auth commandHost) {
	c := &authNKCommand{}

	nk := auth.Command("nkey", "Create and Use NKeys").Alias("nk")

	nkGen := nk.Command("gen", "Generates NKeys").Action(c.genAction)
	nkGen.Arg("type", "Type of key to generate").Required().EnumVar(&c.keyType, "user", "account", "server", "cluster", "operator", "curve", "x25519")
	nkGen.Flag("public", "Output the public key").UnNegatableBoolVar(&c.pubOut)
	nkGen.Flag("entropy", "Source of entropy eg. /dev/urandom").ExistingFileVar(&c.entropySource)
	nkGen.Flag("output", "Write the key to a file").StringVar(&c.outFile)

	nkShow := nk.Command("show", "Show the public key").Action(c.showAction)
	nkShow.Arg("key", "NKey to act on").Required().ExistingFileVar(&c.keyFile)

	nkSign := nk.Command("sign", "Signs data using NKeys").Action(c.signAction)
	nkSign.Arg("file", "File to sign").Required().ExistingFileVar(&c.dataFile)
	nkSign.Arg("key", "NKey to sign with").Required().ExistingFileVar(&c.keyFile)

	nkVerify := nk.Command("verify", "Verify signed data").Action(c.verifyAction)
	nkVerify.Arg("file", "File containing the data to check").Required().ExistingFileVar(&c.dataFile)
	nkVerify.Arg("signature", "File containing the signature").Required().ExistingFileVar(&c.signFile)
	nkVerify.Arg("key", "The key to use for verification").Required().ExistingFileVar(&c.keyFile)
}

func (c *authNKCommand) showAction(_ *fisk.ParseContext) error {
	seed, err := c.readKeyFile(c.keyFile)
	if err != nil {
		return err
	}

	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		log.Fatal(err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return err
	}

	fmt.Println(pub)

	return nil
}

func (c *authNKCommand) genAction(_ *fisk.ParseContext) error {
	prefix, err := c.preForType(c.keyType)
	if err != nil {
		return err
	}

	ef := rand.Reader
	if c.entropySource != "" {
		r, err := os.Open(c.entropySource)
		if err != nil {
			return fmt.Errorf("could not use custom entropy source: %w", err)
		}

		ef = r
	}

	var kp nkeys.KeyPair

	if prefix == nkeys.PrefixByteCurve {
		kp, err = nkeys.CreateCurveKeysWithRand(ef)
	} else {
		kp, err = nkeys.CreatePairWithRand(prefix, ef)
	}
	if err != nil {
		return fmt.Errorf("could not create %q: %w", prefix, err)
	}

	seed, err := kp.Seed()
	if err != nil {
		return err
	}

	out := os.Stdout
	if c.outFile != "" {
		out, err = os.Create(c.outFile)
		if err != nil {
			return err
		}
		defer out.Close()
	}

	_, err = fmt.Fprintln(out, string(seed))
	if err != nil {
		return err
	}

	if c.pubOut {
		pk, err := kp.PublicKey()
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(out, pk)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *authNKCommand) preForType(keyType string) (nkeys.PrefixByte, error) {
	switch strings.ToLower(keyType) {
	case "user":
		return nkeys.PrefixByteUser, nil
	case "account":
		return nkeys.PrefixByteAccount, nil
	case "server":
		return nkeys.PrefixByteServer, nil
	case "cluster":
		return nkeys.PrefixByteCluster, nil
	case "operator":
		return nkeys.PrefixByteOperator, nil
	case "curve", "x25519":
		return nkeys.PrefixByteCurve, nil
	default:
		return nkeys.PrefixByte(0), fmt.Errorf("unknown prefix type %q", keyType)
	}
}

func (c *authNKCommand) readKeyFile(filename string) ([]byte, error) {
	var key []byte
	contents, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	defer c.wipeSlice(contents)

	lines := bytes.Split(contents, []byte("\n"))
	for _, line := range lines {
		if nkeys.IsValidEncoding(line) {
			key = make([]byte, len(line))
			copy(key, line)
			return key, nil
		}
	}
	if key == nil {
		return nil, fmt.Errorf("could not find a valid key in %s", filename)
	}

	return key, nil
}

func (c *authNKCommand) wipeSlice(buf []byte) {
	for i := range buf {
		buf[i] = 'x'
	}
}

func (c *authNKCommand) signAction(_ *fisk.ParseContext) error {
	seed, err := c.readKeyFile(c.keyFile)
	if err != nil {
		return err
	}

	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(c.dataFile)
	if err != nil {
		return err
	}

	sigBytes, err := kp.Sign(content)
	if err != nil {
		return err
	}

	fmt.Println(base64.RawURLEncoding.EncodeToString(sigBytes))

	return nil
}

func (c *authNKCommand) verifyAction(_ *fisk.ParseContext) error {
	var err error
	var kp nkeys.KeyPair

	keyData, err := c.readKeyFile(c.keyFile)
	if err != nil {
		return err
	}

	// try it as public, then as seed
	kp, err = nkeys.FromPublicKey(string(keyData))
	if errors.Is(err, nkeys.ErrInvalidPublicKey) {
		kp, err = nkeys.FromSeed(keyData)
	}
	if err != nil {
		return err
	}

	content, err := os.ReadFile(c.dataFile)
	if err != nil {
		return err
	}

	sigEnc, err := os.ReadFile(c.signFile)
	if err != nil {
		return err
	}

	sig, err := base64.RawURLEncoding.DecodeString(string(sigEnc))
	if err != nil {
		return err
	}

	if err := kp.Verify(content, sig); err != nil {
		return err
	}

	fmt.Println("Verified OK")

	return nil
}
