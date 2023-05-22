// Copyright 2022-2023 Tigris Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/tigrisdata/tigris-cli/client"
	"github.com/tigrisdata/tigris-cli/config"
	"github.com/tigrisdata/tigris-cli/iterate"
	"github.com/tigrisdata/tigris-cli/login"
	"github.com/tigrisdata/tigris-cli/schema"
	"github.com/tigrisdata/tigris-cli/util"
	api "github.com/tigrisdata/tigris-client-go/api/server/v1"
	"github.com/tigrisdata/tigris-client-go/driver"
	cschema "github.com/tigrisdata/tigris-client-go/schema"
)

var (
	NoCreate       bool
	InferenceDepth int32
	PrimaryKey     []string
	AutoGenerate   []string
	UpdateSchema   bool
	Append         bool

	BatchSize int32 = 100

	CleanUpNULLs = true

	CSVDelimiter        string
	CSVComment          string
	CSVTrimLeadingSpace bool
	CSVNoHeader         bool

	sch        cschema.Schema // Accumulate inferred schema across batches
	prevSchema []byte

	ErrIndexShouldExist = fmt.Errorf("index should exist to import CSV with no field names")
	ErrNoAppend         = fmt.Errorf(
		"index exists. use --append if you need to add documents to existing collection")
)

func evolveIdxSchema(ctx context.Context, coll string, docs []json.RawMessage) error {
	// Allow to reduce inference depth in the case of huge batches
	id := len(docs)
	if InferenceDepth > 0 {
		id = int(InferenceDepth)
	}

	err := schema.Infer(&sch, coll, docs, PrimaryKey, AutoGenerate, id)
	util.Fatal(err, "infer schema")

	b, err := json.Marshal(sch)
	util.Fatal(err, "marshal schema: %s", string(b))

	if bytes.Equal(b, prevSchema) {
		return nil
	}

	err = client.GetSearch().CreateOrUpdateIndex(ctx, coll, b)

	return util.Error(err, "create or update index")
}

var importCmd = &cobra.Command{
	Use:   "import {index} {document}...|-",
	Short: "Import documents into search index",
	Long: `Imports documents into the search index.
Input is a stream or array of JSON documents to import.
`,
	Example: fmt.Sprintf(`
  %[1]s search import --project=myproj users --create-index \
  '[
    {"id": 20, "name": "Jania McGrory"},
    {"id": 21, "name": "Bunny Instone"}
  ]'
`, "tigris"),
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		login.Ensure(cmd.Context(), func(ctx context.Context) error {
			resp, err := client.GetSearch().GetIndex(ctx, name)
			found := false
			if err == nil {
				if !Append {
					util.Fatal(ErrNoAppend, "describe collection")
				}
				err = json.Unmarshal(resp.Schema, &sch)
				util.Fatal(err, "unmarshal collection schema")
				found = true
			} else if CSVNoHeader {
				util.Fatal(ErrIndexShouldExist, "get index")
			} else {
				//nolint:golint,errorlint
				ep, ok := err.(*driver.Error)
				if !ok || ep.Code == api.Code_NOT_FOUND && NoCreate {
					return util.Error(err, "import documents get index")
				}
			}

			err = iterate.CSVConfigure(CSVDelimiter, CSVComment, CSVTrimLeadingSpace, CSVNoHeader)
			util.Fatal(err, "csv configure")

			return iterate.Input(cmd.Context(), cmd, 1, args,
				func(ctx context.Context, args []string, docs []json.RawMessage) error {
					ptr := unsafe.Pointer(&docs)

					if UpdateSchema || (!found && !NoCreate) {
						if err = evolveIdxSchema(ctx, name, docs); err != nil {
							return err
						}
					}

					_, err = client.GetSearch().Create(ctx, name, *(*[]driver.Document)(ptr))
					if err == nil {
						return nil // successfully inserted batch
					}

					if CleanUpNULLs {
						for k := range docs {
							docs[k] = util.CleanupNULLValues(docs[k])
						}
					}

					_, err = client.GetSearch().Create(ctx, name, *(*[]driver.Document)(ptr))

					log.Debug().Interface("docs", docs).Msg("import")

					return util.Error(err, "import documents (after schema update")
				})
		})
	},
}

func addProjectFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVarP(&config.DefaultConfig.Project,
		"project", "p", "", "Specifies project: --project=my_proj1")
}

func init() {
	importCmd.Flags().Int32VarP(&BatchSize, "batch-size", "b", BatchSize, "set batch size")
	importCmd.Flags().Int32VarP(&InferenceDepth, "inference-depth", "d", 0,
		"Number of records in the beginning of the stream to detect field types. It's equal to batch size if not set")
	importCmd.Flags().StringSliceVar(&AutoGenerate, "autogenerate", []string{},
		"Comma separated list of autogenerated fields (only top level keys supported)")
	importCmd.Flags().BoolVar(&CleanUpNULLs, "cleanup-null-values", true,
		"Remove NULL values and empty arrays from the documents before importing")
	importCmd.Flags().BoolVar(&UpdateSchema, "update-schema", false,
		"Update index schema from the new documents")

	importCmd.Flags().BoolVarP(&Append, "append", "a", false,
		"Force append to existing index")
	importCmd.Flags().BoolVar(&NoCreate, "no-create-index", false,
		"Do not create collection automatically if it doesn't exist")

	importCmd.Flags().BoolVar(&schema.DetectByteArrays, "detect-byte-arrays", false,
		"Try to detect byte arrays fields")
	importCmd.Flags().BoolVar(&schema.DetectUUIDs, "detect-uuids", true,
		"Try to detect UUID fields")
	importCmd.Flags().BoolVar(&schema.DetectTimes, "detect-times", true,
		"Try to detect date time fields")
	importCmd.Flags().BoolVar(&schema.DetectIntegers, "detect-integers", true,
		"Try to detect integer fields")

	importCmd.Flags().StringVar(&CSVDelimiter, "csv-delimiter", "",
		"CSV delimiter")
	importCmd.Flags().BoolVar(&CSVTrimLeadingSpace, "csv-trim-leading-space", true,
		"Trim leading space in the fields")
	importCmd.Flags().StringVar(&CSVComment, "csv-comment", "",
		"CSV comment")
	addProjectFlag(importCmd)

	RootCmd.AddCommand(importCmd)
}
