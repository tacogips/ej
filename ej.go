package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/ioutil"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/text/language"

	"cloud.google.com/go/translate"
	"github.com/boltdb/bolt"
	"github.com/urfave/cli"
	"google.golang.org/api/option"
)

const EJ_GOOGLE_TRANS_API_KEY_ENV = "EJ_GOOGLE_TRANS_API_KEY"

func main() {
	panic(fmt.Errorf("%#v", fetchDict([]string{"love"})))

	app := cli.NewApp()
	app.Name = "ej"
	app.Description = `simple Japanese <->English translator.
	 once translated result will be cached in local database at "$HOME/.ej"`

	app.Usage = "ej [sentense]"
	app.Commands = nil
	app.Version = "0.0.2"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "c",
			Usage: "list all caches",
		},
		cli.BoolFlag{
			Name:  "f",
			Usage: "force translate. not use cache",
		},
		cli.BoolFlag{
			Name:  "r",
			Usage: "force reverse translation(some language to english) ",
		},
		cli.BoolFlag{
			Name:  "json",
			Usage: "output in json format",
		},
	}

	app.Action = func(c *cli.Context) error {

		var printer func(tr Translate)
		var slicePrinter func(tr []Translate)
		if c.Bool("json") {
			printer = jsonPrinter
			slicePrinter = jsonSlicePrinter
		} else {
			printer = plainPrinter
			slicePrinter = plainSlicePrinter
		}

		dbDir := expandFilePath("$HOME/.ej")
		if _, err := os.Stat(dbDir); os.IsNotExist(err) {
			err := os.MkdirAll(dbDir, 0755)
			if err != nil {
				return err
			}
		}

		db, err := bolt.Open(filepath.Join(dbDir, "ej.db"), 0755, nil)
		if err != nil {
			return err
		}
		defer db.Close()

		if c.Bool("c") {
			var cachedResults []Translate
			err = db.View(func(tx *bolt.Tx) error {
				bucket := tx.Bucket([]byte("cache"))
				err := bucket.ForEach(func(k, v []byte) error {
					cachedResults = append(cachedResults,
						newTranslate(string(k), string(v)))
					return nil
				})
				if err != nil {
					return err
				}

				slicePrinter(cachedResults)
				return nil
			})
			return err
		}

		src := strings.Join(c.Args(), " ")
		if len(src) == 0 {
			return nil
		}
		apiKey := os.Getenv(EJ_GOOGLE_TRANS_API_KEY_ENV)
		if apiKey == "" {
			return fmt.Errorf("need '%s' env variable", EJ_GOOGLE_TRANS_API_KEY_ENV)
		}

		// search from cache
		if !c.Bool("f") {
			// get from cache if exists
			var result string
			err = db.View(func(tx *bolt.Tx) error {
				bucket := tx.Bucket([]byte("cache"))
				if bucket == nil {
					return nil
				}
				val := bucket.Get([]byte(src))
				if len(val) == 0 {
					return nil
				}
				result = string(val)
				return nil
			})

			if err != nil {
				return err
			}
			if result != "" {
				cachedResult := newTranslate(src, result)
				printer(cachedResult)
				return nil
			}
		}

		ctx := context.Background()
		client, err := translate.NewClient(ctx, option.WithAPIKey(apiKey))
		if err != nil {
			return err
		}

		destLang := language.Japanese
		input := []string{src}

		if c.Bool("r") {
			// translate to english if force reverse
			destLang = language.English
		} else {
			// translate to english if input is japanese
			detectedInputLangs, err := client.DetectLanguage(ctx, input)
			if err == nil {
				for _, detectedInputLangsArr := range detectedInputLangs {
					for _, detectedInputLang := range detectedInputLangsArr {
						if detectedInputLang.Language == language.Japanese {
							destLang = language.English
							goto FinishDetectLang
						}
					}
				}
			}

		FinishDetectLang:
		}

		resp, err := client.Translate(ctx, input, destLang, nil)
		if err != nil {
			return err
		}

		result := newTranslate(src, resp[0].Text)
		printer(result)

		err = db.Update(func(tx *bolt.Tx) error {
			bucket, err := tx.CreateBucketIfNotExists([]byte("cache"))
			if err != nil {
				return err
			}
			err = bucket.Put([]byte(result.Input), []byte(result.Translated))

			return err
		})
		if err != nil {
			return err
		}

		return nil
	}

	app.Run(os.Args)
}

var noDefinitionAPIKey = errors.New("no definitiion api key")

func fetchDict(words []string) []Dict {
	var result []Dict
	for _, word := range words {
		defs := readDict(fmt.Sprintf("https://api.datamuse.com/words?sp=%s&md=d", word))
		if len(defs) != 0 {
			syns := readDict(fmt.Sprintf("https://api.datamuse.com/words?rel_syn=%s&md=d", word))
			ants := readDict(fmt.Sprintf("https://api.datamuse.com/words?rel_and=%s&md=d", word))
			result = append(result, Dict{
				Word:       word,
				Definition: defs[0],
				Synonyms:   syns,
				Antonyms:   ants,
			})
		}
	}

	return result
}
func readDict(url string) []Definition {
	r, err := http.Get(url)
	if err != nil {
		return nil
	}
	defer r.Body.Close()

	d, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil
	}

	var defs []Definition
	err = json.Unmarshal(d, &defs)
	if err != nil {
		panic(err)
	}

	return defs
}

type Definition struct {
	Word string   `json:"word"`
	Defs []string `json:"defs"`
}

type Dict struct {
	Word       string       `json:"word"`
	Definition Definition   `json:"definition"`
	Synonyms   []Definition `json:"synonyms"`
	Antonyms   []Definition `json:"antonyms"`
}

func expandFilePath(p string) string {
	trimPath := strings.TrimSpace(p)
	isAbs := filepath.IsAbs(trimPath)
	plainsDirs := strings.Split(trimPath, "/")

	var dirs []string

	for _, plainDir := range plainsDirs {

		if len(plainDir) == 0 {
			continue
		}
		if plainDir == "~" {
			usr, err := user.Current()
			if err != nil {
				panic(err)
			}
			dirs = append(dirs, usr.HomeDir)
		} else if plainDir[0] == '$' {
			dirs = append(dirs, os.Getenv(plainDir[1:]))
		} else {
			dirs = append(dirs, plainDir)
		}
	}

	if isAbs {
		paths := append([]string{"/"}, dirs...)
		absp, err := filepath.Abs(filepath.Join(paths...))
		if err != nil {
			panic(err)
		}
		return absp

	} else {
		absp, err := filepath.Abs(filepath.Join(dirs...))
		if err != nil {
			panic(err)
		}
		return absp
	}

}

type Translate struct {
	Input      string `json:"input"`
	Translated string `json:"translated"`
	Dicts      []Dict `json:"dicts"`
}

func newTranslate(input string, translated string) Translate {
	return Translate{
		Input:      html.UnescapeString(input),
		Translated: html.UnescapeString(translated),
	}
}

func plainPrinter(tr Translate) {
	fmt.Fprintf(os.Stdout, "%s\n%s\n\n", tr.Input, tr.Translated)
}

func plainSlicePrinter(tr []Translate) {
	for _, each := range tr {
		plainPrinter(each)
	}
}
func jsonPrinter(tr Translate) {
	j, err := json.Marshal(tr)
	if err == nil {
		fmt.Fprintf(os.Stdout, "%s\n", string(j))
	}
}

func jsonSlicePrinter(tr []Translate) {
	j, err := json.Marshal(tr)
	if err == nil {
		fmt.Fprintf(os.Stdout, "%s\n", string(j))
	}
}
