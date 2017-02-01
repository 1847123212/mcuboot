// The image tool.
//
// A standalone tool to manipulate images.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"strings"
	"text/template"

	log "github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
)

var keyFile string
var keyType KeyGenerator

func main() {
	root := &cobra.Command{
		Use:   "imgtool command args ...",
		Short: "Manipulate boot images",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Usage()
			log.Fatal("Invalid usage")
		},
	}

	fl := root.PersistentFlags()
	fl.StringVarP(&keyFile, "key", "k", "root_ec.pem", "Keyfile to use")

	keygen := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an ECDSA P-256 private key",
		Run:   doKeyGen,
	}

	fl = keygen.Flags()
	fl.VarP(&keyType, "key-type", "t", "Type of key to generate")

	root.AddCommand(keygen)

	getpub := &cobra.Command{
		Use:   "getpub",
		Short: "Extract the public key as C code",
		Run:   doGetPub,
	}

	root.AddCommand(getpub)

	root.AddCommand(setupSign())

	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func doKeyGen(cmd *cobra.Command, args []string) {
	if keyType.generate == nil {
		cmd.Usage()
		log.Fatal("Must specify key type with --key-type")
	}

	if len(args) != 0 {
		cmd.Usage()
		log.Fatal("Expecting no arguments to keygen")
	}

	priv509, err := keyType.generate()
	if err != nil {
		log.Fatal(err)
	}

	fd, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer fd.Close()

	block := pem.Block{
		Type:  keyType.pemType,
		Bytes: priv509,
	}
	err = pem.Encode(fd, &block)
	if err != nil {
		log.Fatal(err)
	}
}

var keyGens map[string]*KeyGenerator

type KeyGenerator struct {
	name        string
	description string
	pemType     string
	generate    func() ([]byte, error)
}

func (g *KeyGenerator) Set(text string) error {
	kg, ok := keyGens[text]
	if !ok {
		return errors.New("Unsupported key type")
	}

	*g = *kg
	return nil
}

func (g *KeyGenerator) String() string {
	return g.name
}

func (g *KeyGenerator) Type() string {
	return "keytype"
}

func init() {
	keyGens = make(map[string]*KeyGenerator)

	kg := &KeyGenerator{
		name:        "ecdsa-p256",
		description: "ECDSA with SHA256 and the NIST P-256 curve",
		pemType:     "EC PRIVATE KEY",
		generate:    genEcdsaP256,
	}
	keyGens[kg.name] = kg

	kg = &KeyGenerator{
		name:        "ecdsa-p224",
		description: "ECDSA with SHA256 and the NIST P-224 curve",
		pemType:     "EC PRIVATE KEY",
		generate:    genEcdsaP224,
	}
	keyGens[kg.name] = kg

	kg = &KeyGenerator{
		name:        "rsa-2048",
		description: "RSA 2048",
		pemType:     "RSA PRIVATE KEY",
		generate:    genRSA2048,
	}
	keyGens[kg.name] = kg
}

func genEcdsaP224() ([]byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		return nil, err
	}

	return x509.MarshalECPrivateKey(priv)
}

func genEcdsaP256() ([]byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	return x509.MarshalECPrivateKey(priv)
}

func genRSA2048() ([]byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	return x509.MarshalPKCS1PrivateKey(priv), nil
}

func doGetPub(cmd *cobra.Command, args []string) {
	data, err := ioutil.ReadFile(keyFile)
	if err != nil {
		log.Fatal(err)
	}

	block, data := pem.Decode(data)

	// openssl will sometimes generate this extra parameters
	// fields at the top (although it is included in the private
	// key as well).  If we see this, just read the next block.
	if block.Type == "EC PARAMETERS" {
		block, data = pem.Decode(data)
	}
	// fmt.Printf("type=%q, headers=%v, data=\n%s", block.Type, block.Headers, hex.Dump(block.Bytes))

	if block.Type == "EC PRIVATE KEY" {
		dumpECPub(block)
	} else if block.Type == "RSA PRIVATE KEY" {
		dumpRSAPub(block)
	} else {
		log.Fatal("Only supports ECDSA and RSA keys")
	}
}

func dumpECPub(block *pem.Block) {
	privateKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		log.Fatal(err)
	}
	// fmt.Printf("priv: %+v\n", privateKey)

	// Dump out the public key as a nice structure.
	// fmt.Printf("x = %x\n", privateKey.X.Bytes())
	// fmt.Printf("y = %x\n", privateKey.Y.Bytes())

	// tdata := make(map[string]string)
	// tdata["x"] = formatCData(privateKey.X.Bytes(), 2)
	// tdata["y"] = formatCData(privateKey.Y.Bytes(), 2)
	// err = ecKeyTemplate.Execute(os.Stdout, tdata)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// The public key needs the algorithm and curve parameters.
	var curve []int
	switch privateKey.Params().Name {
	case "P-224":
		curve = []int{1, 3, 132, 0, 33}
	case "P-256":
		curve = []int{1, 2, 840, 10045, 3, 1, 7}
	default:
		log.Fatal("Key uses unsupported curve: %q", privateKey.Params().Name)
	}

	// The public key is encoded uncompressed, as a concatenation
	// of the bytes.
	var bbuf bytes.Buffer
	bbuf.WriteByte(0x04)
	bbuf.Write(privateKey.X.Bytes())
	bbuf.Write(privateKey.Y.Bytes())
	pkeyBytes := bbuf.Bytes()

	pkey := EcPublicKey{
		Algorithm: AlgorithmId{
			Algorithm: []int{1, 2, 840, 10045, 2, 1},
			Curve:     curve,
		},
		PubKey: asn1.BitString{
			Bytes:     pkeyBytes,
			BitLength: len(pkeyBytes) * 8,
		},
	}
	asnBytes, err := asn1.Marshal(pkey)
	if err != nil {
		log.Fatal(err)
	}
	// fmt.Print(hex.Dump(asnBytes))
	fmt.Printf(`/* Autogenerated, do not edit */

const unsigned char ec_pub_key[] = {
	%s };
const unsigned int ec_pub_key_len = %d;
`,
		formatCData(asnBytes, 1), len(asnBytes))
}

func dumpRSAPub(block *pem.Block) {
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		log.Fatal(err)
	}

	pubKey := RSAPublicKey{
		N: privateKey.N,
		E: privateKey.E,
	}

	asnBytes, err := asn1.Marshal(pubKey)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf(`/* Autogenerated, do not edit */

const unsigned char rsa_pub_key[] = {
	%s };
const unsigned int ec_pub_key_len = %d;
`,
		formatCData(asnBytes, 1), len(asnBytes))
}

// ecPublicKey represents an ASN.1 Elliptic Curve Public Key structure
type EcPublicKey struct {
	Algorithm AlgorithmId
	PubKey    asn1.BitString
}

type RSAPublicKey struct {
	N *big.Int
	E int
}

type AlgorithmId struct {
	Algorithm asn1.ObjectIdentifier
	Curve     asn1.ObjectIdentifier
}

// Format a byte slice as 'C' data, with the given indentation on
// subsequent lines.
func formatCData(data []byte, indent int) string {
	buf := new(bytes.Buffer)

	indText := strings.Repeat("\t", indent)

	for i, b := range data {
		if i%8 == 0 {
			if i > 0 {
				fmt.Fprintf(buf, "\n%s", indText)
			}
		} else {
			fmt.Fprintf(buf, " ")
		}
		fmt.Fprintf(buf, "0x%02x,", b)

	}

	return buf.String()
}

var ecKeyTemplate = template.Must(template.New("eckey").Parse(`
/* Autogenerated, do not edit. */

#include <stdint.h>

struct ec_key {
	uint8_t x[32];
	uint8_t y[32];
};

struct ec_key ec_pub_key = {
	.x = {  {{.x}} },
	.y = {  {{.y}} },
};
`))
