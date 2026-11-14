package main

import (
	"context"
	"fmt"
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

func main() {
	app := cli.NewApp()
	app.Name = "ej"
	app.Description = "simple transrator"
	app.Usage = "ej [-c] sentense"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "c",
			Usage: "list all caches",
		},
	}

	app.Action = func(c *cli.Context) error {

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
			err = db.View(func(tx *bolt.Tx) error {
				bucket := tx.Bucket([]byte("cache"))
				return bucket.ForEach(func(k, v []byte) error {
					fmt.Printf("%s\n%s\n\n", string(k), string(v))
					return nil
				})
			})
			return err
		}

		src := strings.Join(c.Args(), " ")
		if len(src) == 0 {
			return nil
		}
		apiKey := os.Getenv("EJ_API_KEY")
		if apiKey == "" {
			return fmt.Errorf("need 'EJ_API_KEY' env variable")
		}

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
			fmt.Printf("%s\n%s\n", src, result)
			return nil
		}

		ctx := context.Background()
		client, err := translate.NewClient(ctx, option.WithAPIKey(apiKey))
		if err != nil {
			return err
		}

		resp, err := client.Translate(ctx, []string{src}, language.Japanese, nil)
		if err != nil {
			return err
		}

		fmt.Printf("%s\n%s\n", src, resp[0].Text)

		err = db.Update(func(tx *bolt.Tx) error {
			bucket, err := tx.CreateBucketIfNotExists([]byte("cache"))
			if err != nil {
				return err
			}
			err = bucket.Put([]byte(src), []byte(resp[0].Text))

			return err
		})
		if err != nil {
			return err
		}

		return nil
	}

	app.Run(os.Args)
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
