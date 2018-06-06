// this source code is naive ugly due to intention of private use. i dont wanna type keys many time.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/language"

	"cloud.google.com/go/translate"
	"github.com/boltdb/bolt"
	"github.com/urfave/cli"
	"google.golang.org/api/option"
)

const (
	EJ_GOOGLE_TRANS_API_KEY_ENV = "EJ_GOOGLE_TRANS_API_KEY"
	BOLT_TRANSLATE_BUCKET       = "trans_cache"
	BOLT_DICT_BUCKET            = "dict_cache"
	BOLT_URBAN_DICT_BUCKET      = "urban_dict_cache"

	MAX_FETCH_DICT_WORD_NUM_AT_ONE = 4

	MAX_FETCH_DEF_NUM = 3

	MAX_URBAN_DICT_RESULT_NUM = 4
)

var dictDefSanitizer = regexp.MustCompile("\t")

func main() {
	app := cli.NewApp()
	app.Name = "ej"
	app.Description = `simple Japanese <->English translator.
	 once translated result will be cached in local database at "$HOME/.ej"`

	app.Usage = "ej [sentense]"
	app.Commands = nil
	app.Version = "0.0.5"
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
			Name:  "m",
			Usage: "merge json cache file from stdin ",
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

		if c.Bool("m") { // merge cache file
			jsonCache, err := readFromStdin()
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read json cache %+v \n", err)
				return err
			}

			cache := []TranslateAndDicts{}
			err = json.Unmarshal(jsonCache, &cache)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to unmarshall json cache %+v \n", err)
				return err
			}
			for _, eachTran := range cache {

				err = putTranslationToCache(cacheDB, eachTran.Translate)
				if err != nil {
					fmt.Fprintf(os.Stderr, "failed to merge tlanslate %+v \n", err)
				}

				err = putUrbanDictToCache(cacheDB, eachTran.UrbanDict)
				if err != nil {
					fmt.Fprintf(os.Stderr, "failed to merge urban dict %+v \n", err)
				}
				for _, dict := range eachTran.WordDicts {
					err = putDictToCache(cacheDB, dict)
					if err != nil {
						fmt.Fprintf(os.Stderr, "failed to merge word dict %+v \n", err)
					}
				}
			}

			return nil
		}

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

				t.Translate.RefNum = t.Translate.RefNum + 1
				t.Translate.LastReferedAt = time.Now().Unix()
				err = putTranslationToCache(cacheDB, t.Translate)

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

		var urbanDict UrbanDict
		if !noDict {
			if destLang == language.English {
				urbanDict = fetchUrbanDict(cacheDB, translated.Translated, false, notUseCache)
			} else if inputLang == language.English {
				urbanDict = fetchUrbanDict(cacheDB, translated.Input, false, notUseCache)
			}
		}

		var dicts []WordDict
		if !noDict {
			if destLang == language.English {
				dicts = fetchDictOfWords(cacheDB, translated.Translated, false, notUseCache)
			} else if inputLang == language.English {
				dicts = fetchDictOfWords(cacheDB, translated.Input, false, notUseCache)
			}
		}

		result := TranslateAndDicts{
			Translate: translated,
			UrbanDict: urbanDict,
			WordDicts: dicts,
		}

		printer(result)
		return nil
	}

	if err := app.Run(os.Args); err != nil {
		panic(err)
	}
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
	sort.Slice(cachedResults, func(i, j int) bool {
		return cachedResults[i].Translate.LastReferedAt > cachedResults[j].Translate.LastReferedAt
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
					result.UrbanDict = fetchUrbanDict(db, tr.Input, true, false)
					result.WordDicts = fetchDictOfWords(db, tr.Input, true, false)
				} else if tr.IsTranslatedIsEng() {
					result.UrbanDict = fetchUrbanDict(db, tr.Translated, true, false)
					result.WordDicts = fetchDictOfWords(db, tr.Translated, true, false)
				}
			}

			found = true
		}
		return nil
	})

	return result, found
}

func fetchUrbanDict(db *bolt.DB, engSentense string, onlyFromCache bool, notUseCache bool) UrbanDict {

	var fetchFromUrban = func(engSentense string) (UrbanDict, bool) {

		url := fmt.Sprintf("http://api.urbandictionary.com/v0/define?term=%s", url.QueryEscape(engSentense))

		r, err := http.Get(url)
		if err != nil {
			return UrbanDict{}, false
		}
		defer r.Body.Close()

		d, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return UrbanDict{}, false
		}

		var dict UrbanDict
		err = json.Unmarshal(d, &dict)
		if err != nil {
			panic(err)
		}

		dict.Input = engSentense

		if len(dict.UrbanDictList) > MAX_URBAN_DICT_RESULT_NUM {
			dict.UrbanDictList = dict.UrbanDictList[:MAX_URBAN_DICT_RESULT_NUM]
		}

		return dict, true
	}

	if notUseCache {
		if d, ok := fetchFromUrban(engSentense); ok {
			putUrbanDictToCache(db, d)
			return d
		}

	} else {
		if d, onCache := fetchUrbanDictFromCache(db, engSentense); onCache {
			return d
		} else if !onlyFromCache {
			if d, ok := fetchFromUrban(engSentense); ok {
				putUrbanDictToCache(db, d)
				return d
			}
		}
	}
	return UrbanDict{}
}

func fetchDictOfWords(db *bolt.DB, engSentense string, onlyFromCache bool, notUseCache bool) []WordDict {
	var result []WordDict

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

func fetchUrbanDictFromCache(db *bolt.DB, input string) (UrbanDict, bool) {
	var d UrbanDict
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BOLT_URBAN_DICT_BUCKET))
		if bucket == nil {
			return nil
		}

		val := bucket.Get([]byte(input))
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
	return UrbanDict{}, false
}

func fetchDictFromCache(db *bolt.DB, word string) (WordDict, bool) {
	var d WordDict
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
	return WordDict{}, false
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

func putUrbanDictToCache(db *bolt.DB, d UrbanDict) error {
	return db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(BOLT_URBAN_DICT_BUCKET))
		if err != nil {
			return err
		}
		mashalled, err := json.Marshal(d)
		if err != nil {
			return err
		}
		err = bucket.Put([]byte(d.Input), mashalled)
		return err
	})
}

func putDictToCache(db *bolt.DB, d WordDict) error {
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

func fetchDictFromAPI(word string) (WordDict, bool) {
	baseURL := "https://api.datamuse.com/words"
	defs := readDef(fmt.Sprintf(baseURL+"?sp=%s&md=d&max=%d", word, MAX_FETCH_DEF_NUM))

	if len(defs) != 0 && len(defs[0].Defs) != 0 {
		syns := readDef(fmt.Sprintf(baseURL+"?rel_syn=%s&md=d&max=%d", word, MAX_FETCH_DEF_NUM))
		ants := readDef(fmt.Sprintf(baseURL+"?rel_ant=%s&md=d&max=%d", word, MAX_FETCH_DEF_NUM))

		return WordDict{
			Word:       word,
			Definition: defs[0],
			Synonyms:   syns,
			Antonyms:   ants,
		}, true
	}

	return WordDict{}, false
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

type UrbanDict struct {
	Input         string         `json:"input"`
	Tags          []string       `json:"tags"`
	UrbanDictList []UrbanDictDef `json:"list"`
}

type UrbanDictDef struct {
	Word       string `json:"word"`
	Definition string `json:"definition"`
	Example    string `json:"example"`
}

type WordDict struct {
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
	Translate Translate  `json:"translate"`
	UrbanDict UrbanDict  `json:"urban_dict"`
	WordDicts []WordDict `json:"dicts"`
}

type Translate struct {
	Input          string `json:"input"`
	InputLang      string `json:"input_lang"`
	Translated     string `json:"translated"`
	TranslatedLang string `json:"translated_lang"`
	RefNum         int    `json:"ref_num,omitempty"`
	LastReferedAt  int64  `json:"last_refered_at,omitempty"`
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
		RefNum:         1,
		LastReferedAt:  time.Now().Unix(),
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
	fmt.Fprintf(os.Stdout, "%s\n%s\nsearched %d times \n", tr.Translate.Input, tr.Translate.Translated, tr.Translate.RefNum+1)

	// show urban
	fmt.Fprintf(os.Stdout, "==== urban tags <%s> \n", strings.Join(tr.UrbanDict.Tags, ", "))

	for idx, urban := range tr.UrbanDict.UrbanDictList {
		fmt.Fprintf(os.Stdout, " \n---- urban no %d  ------------------------- \n", idx+1)
		fmt.Fprintf(os.Stdout, "word:<%s> \n", urban.Word)
		fmt.Fprintf(os.Stdout, "def:\n")
		fmt.Fprintf(os.Stdout, "%s \n", urban.Definition)

		fmt.Fprintf(os.Stdout, "e.g \n")
		fmt.Fprintf(os.Stdout, "%s \n", urban.Example)
	}

	fmt.Fprintf(os.Stdout, "\n==== word dect \n")

	for _, d := range tr.WordDicts {

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

func readFromStdin() ([]byte, error) {
	r := bufio.NewReader(os.Stdin)
	input, _, err := r.ReadLine()
	if err != nil {
		return nil, err
	}
	return input, nil
}
