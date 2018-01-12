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
	"regexp"
	"strings"

	"golang.org/x/text/language"

	"cloud.google.com/go/translate"
	"github.com/boltdb/bolt"
	"github.com/urfave/cli"
	"google.golang.org/api/option"
)

const EJ_GOOGLE_TRANS_API_KEY_ENV = "EJ_GOOGLE_TRANS_API_KEY"
const (
	BOLT_TRANSLATE_BUCKET = "trans_cache"
	BOLT_DICT_BUCKET      = "dict_cache"

	MAX_FETCH_DICT_WORD_NUM_AT_ONE = 4

	MAX_FETCH_DEF_NUM = 3
)

var dictDefSanitizer = regexp.MustCompile("\t")

func main() {
	app := cli.NewApp()
	app.Name = "ej"
	app.Description = `simple Japanese <->English translator.
	 once translated result will be cached in local database at "$HOME/.ej"`

	app.Usage = "ej [sentense]"
	app.Commands = nil
	app.Version = "0.0.4"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "l",
			Usage: "list all caches",
		},
		cli.BoolFlag{
			Name:  "nd",
			Usage: "no dictionry",
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

		var printer func(tr TranslateAndDicts)
		var slicePrinter func(tr []TranslateAndDicts)

		// set printer
		if c.Bool("json") {
			printer = jsonPrinter
			slicePrinter = jsonSlicePrinter
		} else {
			printer = plainPrinter
			slicePrinter = plainSlicePrinter
		}

		cacheDB, err := loadCacheDB()
		if err != nil {
			return err
		}
		defer cacheDB.Close()

		noDict := c.Bool("nd")
		if c.Bool("l") { // list cached
			slicePrinter(fetchCacheList(cacheDB, noDict))
			return nil
		}

		src := strings.Join(c.Args(), " ")
		if len(src) == 0 {
			return nil
		}

		notUseCache := c.Bool("f")
		// search from cache
		if !notUseCache {
			t, found := fetchTranslationFromCache(cacheDB, src, noDict)
			if found {
				printer(t)
				return nil
			}
		}

		apiKey := os.Getenv(EJ_GOOGLE_TRANS_API_KEY_ENV)
		if apiKey == "" {
			return fmt.Errorf("need '%s' env variable", EJ_GOOGLE_TRANS_API_KEY_ENV)
		}

		ctx := context.Background()
		client, err := translate.NewClient(ctx, option.WithAPIKey(apiKey))
		if err != nil {
			return err
		}

		// default , somelang to japanese
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

		inputLang := resp[0].Source
		translated := newTranslate(inputLang, src, destLang, resp[0].Text)
		err = putTranslationToCache(cacheDB, translated)
		if err != nil {
			return err
		}

		var dicts []Dict
		if !noDict {
			if destLang == language.English {
				dicts = fetchDictOfWords(cacheDB, translated.Translated, false, notUseCache)
			} else if inputLang == language.English {
				dicts = fetchDictOfWords(cacheDB, translated.Input, false, notUseCache)
			}
		}

		result := TranslateAndDicts{
			Translate: translated,
			Dicts:     dicts,
		}

		printer(result)
		return nil
	}

	app.Run(os.Args)
}

var noDefinitionAPIKey = errors.New("no definitiion api key")

func loadCacheDB() (*bolt.DB, error) {
	dbDir := expandFilePath("$HOME/.ej")
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		err := os.MkdirAll(dbDir, 0755)
		if err != nil {
			return nil, err
		}
	}

	db, err := bolt.Open(filepath.Join(dbDir, "ej.db"), 0755, nil)
	return db, err
}

func fetchCacheList(db *bolt.DB, noDict bool) []TranslateAndDicts {
	var cachedResults []TranslateAndDicts
	db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BOLT_TRANSLATE_BUCKET))
		bucket.ForEach(func(k, v []byte) error {
			if c, ok := fetchTranslationFromCache(db, string(k), noDict); ok {
				cachedResults = append(cachedResults, c)
			}
			return nil
		})

		return nil
	})
	return cachedResults
}

func fetchTranslationFromCache(db *bolt.DB, src string, noDict bool) (TranslateAndDicts, bool) {
	// get from cache if exists
	result := TranslateAndDicts{}
	found := false
	db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BOLT_TRANSLATE_BUCKET))
		if bucket == nil {
			return nil
		}
		v := bucket.Get([]byte(src))
		if len(v) == 0 {
			return nil
		}

		var tr Translate
		if err := json.Unmarshal(v, &tr); err == nil {
			result.Translate = tr
			if !noDict {
				if tr.IsInputIsEng() {
					result.Dicts = fetchDictOfWords(db, tr.Input, true, false)
				} else if tr.IsTranslatedIsEng() {
					result.Dicts = fetchDictOfWords(db, tr.Translated, true, false)
				}
			}

			found = true
		}
		return nil
	})

	return result, found
}

func fetchDictOfWords(db *bolt.DB, engSentense string, onlyFromCache bool, notUseCache bool) []Dict {
	var result []Dict

	words := strings.Split(engSentense, " ")
	if len(words) > MAX_FETCH_DICT_WORD_NUM_AT_ONE {
		words = words[:MAX_FETCH_DICT_WORD_NUM_AT_ONE]
	}

	for _, word := range words {
		if word == "" {
			continue
		}

		if notUseCache {
			if d, ok := fetchDictFromAPI(word); ok {
				putDictToCache(db, d)
				result = append(result, d)
			}
		} else {
			if d, onCache := fetchDictFromCache(db, word); onCache {
				result = append(result, d)
			} else if !onlyFromCache {
				if d, ok := fetchDictFromAPI(word); ok {
					putDictToCache(db, d)
					result = append(result, d)
				}
			}
		}
	}
	return result
}

func fetchDictFromCache(db *bolt.DB, word string) (Dict, bool) {
	var d Dict
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BOLT_DICT_BUCKET))
		if bucket == nil {
			return nil
		}

		val := bucket.Get([]byte(word))
		if len(val) == 0 {
			return errors.New("not found")
		}

		err := json.Unmarshal(val, &d)
		if err != nil {
			return err
		}
		return nil
	})

	if err == nil {
		return d, true
	}
	return Dict{}, false
}

func putTranslationToCache(db *bolt.DB, tr Translate) error {
	return db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(BOLT_TRANSLATE_BUCKET))
		if err != nil {
			return err
		}
		d, err := json.Marshal(tr)
		if err != nil {
			return err
		}
		err = bucket.Put([]byte(tr.Input), d)
		return err
	})
}

func putDictToCache(db *bolt.DB, d Dict) error {
	return db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(BOLT_DICT_BUCKET))
		if err != nil {
			return err
		}
		mashalled, err := json.Marshal(d)
		if err != nil {
			return err
		}
		err = bucket.Put([]byte(d.Word), mashalled)
		return err
	})
}

func fetchDictFromAPI(word string) (Dict, bool) {
	baseURL := "https://api.datamuse.com/words"
	defs := readDef(fmt.Sprintf(baseURL+"?sp=%s&md=d&max=%d", word, MAX_FETCH_DEF_NUM))

	if len(defs) != 0 && len(defs[0].Defs) != 0 {
		syns := readDef(fmt.Sprintf(baseURL+"?rel_syn=%s&md=d&max=%d", word, MAX_FETCH_DEF_NUM))
		ants := readDef(fmt.Sprintf(baseURL+"?rel_ant=%s&md=d&max=%d", word, MAX_FETCH_DEF_NUM))

		return Dict{
			Word:       word,
			Definition: defs[0],
			Synonyms:   syns,
			Antonyms:   ants,
		}, true
	}

	return Dict{}, false
}

func readDef(url string) []Definition {
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

	if len(defs) > MAX_FETCH_DEF_NUM {
		defs = defs[:MAX_FETCH_DEF_NUM]
	}

	for i, def := range defs {
		if len(def.Defs) > MAX_FETCH_DEF_NUM {
			def.Defs = def.Defs[:MAX_FETCH_DEF_NUM]
		}
		for di, d := range def.Defs {
			def.Defs[di] = dictDefSanitizer.ReplaceAllString(d, " ")
		}
		defs[i] = def
	}

	return defs
}

type Dict struct {
	Word       string       `json:"word"`
	Definition Definition   `json:"definition"`
	Synonyms   []Definition `json:"synonyms"`
	Antonyms   []Definition `json:"antonyms"`
}

type Definition struct {
	Word string   `json:"word"`
	Defs []string `json:"defs"`
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

type TranslateAndDicts struct {
	Translate Translate `json:"translate"`
	Dicts     []Dict    `json:"dicts"`
}

type Translate struct {
	Input          string `json:"input"`
	InputLang      string `json:"input_lang"`
	Translated     string `json:"translated"`
	TranslatedLang string `json:"translated_lang"`
}

func (tr Translate) IsInputIsEng() bool {
	return tr.InputLang == language.English.String()
}

func (tr Translate) IsTranslatedIsEng() bool {
	return tr.TranslatedLang == language.English.String()
}

func newTranslate(inputLang language.Tag, input string, translatedLang language.Tag, translated string) Translate {
	return Translate{
		Input:          html.UnescapeString(input),
		InputLang:      inputLang.String(),
		Translated:     html.UnescapeString(translated),
		TranslatedLang: translatedLang.String(),
	}
}

func plainPrinterDefinition(prefix string, def Definition) {
	type Definition struct {
		Word string   `json:"word"`
		Defs []string `json:"defs"`
	}

	fmt.Fprintf(os.Stdout, "%s<%s>\n", prefix, def.Word)
	for _, txt := range def.Defs {
		fmt.Fprintf(os.Stdout, "%s   (def) %s\n", prefix, txt)
	}
	fmt.Fprint(os.Stdout, "\n")
}

func plainPrinter(tr TranslateAndDicts) {
	fmt.Fprintf(os.Stdout, "%s\n%s\n", tr.Translate.Input, tr.Translate.Translated)
	for _, d := range tr.Dicts {

		type Dict struct {
			Word       string       `json:"word"`
			Definition Definition   `json:"definition"`
			Synonyms   []Definition `json:"synonyms"`
			Antonyms   []Definition `json:"antonyms"`
		}

		plainPrinterDefinition("  [word] ", d.Definition)
		for _, syn := range d.Synonyms {
			plainPrinterDefinition("    [syn] ", syn)
		}

		for _, ant := range d.Antonyms {
			plainPrinterDefinition("    [ant] ", ant)
		}
	}
}

func plainSlicePrinter(tr []TranslateAndDicts) {
	for _, each := range tr {
		plainPrinter(each)
	}
}
func jsonPrinter(tr TranslateAndDicts) {
	j, err := json.Marshal(tr)
	if err == nil {
		fmt.Fprintf(os.Stdout, "%s\n", string(j))
	}
}

func jsonSlicePrinter(tr []TranslateAndDicts) {
	j, err := json.Marshal(tr)
	if err == nil {
		fmt.Fprintf(os.Stdout, "%s\n", string(j))
	}
}
