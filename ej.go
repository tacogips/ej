package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/text/language"

	rapidAPISDK "github.com/RapidSoftwareSolutions/rapidapi-go-sdk/RapidAPISDK"

	"cloud.google.com/go/translate"
	"github.com/boltdb/bolt"
	"github.com/urfave/cli"
	"google.golang.org/api/option"
)

const EJ_GOOGLE_TRANS_API_KEY_ENV = "EJ_GOOGLE_TRANS_API_KEY"
const EJ_RAPID_EPI_KEY_ENV = "EJ_RAPID_EPI_KEY"
const EJ_RAPID_API_PROJECT_NAME_ENV = "EJ_RAPID_API_PROJECT_NAME"

func main() {
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

func fetchDict(words []string) ([]Dict, error) {

	apiKey := os.Getenv(EJ_RAPID_EPI_KEY_ENV)
	if apiKey == "" {
		return nil, noDefinitionAPIKey
	}

	projectName := os.Getenv(EJ_RAPID_API_PROJECT_NAME_ENV)
	if projectName == "" {
		return nil, noDefinitionAPIKey
	}

	api := rapidAPISDK.RapidAPI{
		Project: projectName,
		Key:     apiKey,
	}
	var result []Dict
	for _, word := range words {

		for _, caller := range dictCallers {
			callResp := dictCall(api, word, caller)
			switch r := callResp.(type) {
			}
		}
	}

	return result, nil
}

type dictCaller struct {
	pack   string
	block  string
	opResp func(map[string]interface{}) interface{}
}

var dictCallers = []dictCaller{
	{
		pack:  "WORDS",
		block: "",
		opResp: func(resp map[string]interface{}) interface{} {
			//TODO
			panic(resp)
		},
	},
}

func dictCall(api rapidAPISDK.RapidAPI, word string, caller dictCaller) interface{} {
	params := map[string]rapidAPISDK.Param{
		"word": {
			Type:  "string",
			Value: word,
		},
	}

	return caller.opResp(api.Call(caller.pack, caller.block, params))
}

type Dict struct {
	Word        string   `json:"word"`
	Definitions []string `json:"definitions"`
	Synonyms    []string `json:"synonyms"`
	Antonyms    []string `json:"anotonyms"`
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
