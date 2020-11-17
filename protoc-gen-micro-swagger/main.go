package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/BlackCodes/micro-swagger/protoc-gen-micro-swagger/internal/codegenerator"
	"github.com/BlackCodes/micro-swagger/protoc-gen-micro-swagger/internal/descriptor"
	"github.com/BlackCodes/micro-swagger/protoc-gen-micro-swagger/internal/genopenapi"
	"github.com/golang/glog"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

var (
	importPrefix               = flag.String("import_prefix", "", "prefix to be added to go package paths for imported proto files")
	file                       = flag.String("file", "-", "where to load data from")
	allowDeleteBody            = flag.Bool("allow_delete_body", false, "unless set, HTTP DELETE methods may not have a body")
	allowMerge                 = flag.Bool("allow_merge", false, "if set, generation one OpenAPI file out of multiple protos")
	mergeFileName              = flag.String("merge_file_name", "apioptionss", "target OpenAPI file name prefix after merge")
	useJSONNamesForFields      = flag.Bool("json_names_for_fields", true, "if disabled, the original proto name will be used for generating OpenAPI definitions")
	repeatedPathParamSeparator = flag.String("repeated_path_param_separator", "csv", "configures how repeated fields should be split. Allowed values are `csv`, `pipes`, `ssv` and `tsv`.")
	versionFlag                = flag.Bool("version", false, "print the current version")
	allowRepeatedFieldsInBody  = flag.Bool("allow_repeated_fields_in_body", false, "allows to use repeated field in `body` and `response_body` field of `google.api.http` annotation option")
	includePackageInTags       = flag.Bool("include_package_in_tags", false, "if unset, the gRPC service name is added to the `Tags` field of each operation. if set and the `package` directive is shown in the proto file, the package name will be prepended to the service name")
	useFQNForOpenAPIName       = flag.Bool("fqn_for_openapi_name", false, "if set, the object's OpenAPI names will use the fully qualify name from the proto definition (ie my.package.MyMessage.MyInnerMessage")
	useGoTemplate              = flag.Bool("use_go_templates", false, "if set, you can use Go templates in protofile comments")
	disableDefaultErrors       = flag.Bool("disable_default_errors", false, "if set, disables generation of default errors. This is useful if you have defined custom error handling")
	enumsAsInts                = flag.Bool("enums_as_ints", false, "whether to render enum values as integers, as opposed to string values")
	simpleOperationIDs         = flag.Bool("simple_operation_ids", false, "whether to remove the service prefix in the operationID generation. Can introduce duplicate operationIDs, use with caution.")
	generateUnboundMethods     = flag.Bool("generate_unbound_methods", false, "generate swagger metadata even for RPC methods that have no HttpRule annotation")
	optionsServerPort          = flag.Int64("doc_server_port", 9099, "generate swagger optionsument push to remote options server")
	optionsServer              = flag.String("doc_server", "0.0.0.0", "generate swagger optionsument push to remote options server")
	project                    = flag.String("project", "", "the proto belong to which project")
)

// Variables set by goreleaser at build time
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	flag.Parse()
	defer glog.Flush()

	if *versionFlag {
		fmt.Printf("Version %v, commit %v, built at %v\n", version, commit, date)
		os.Exit(0)
	}

	reg := descriptor.NewRegistry()

	glog.V(1).Info("Processing code generator request")
	f := os.Stdin
	if *file != "-" {
		var err error
		f, err = os.Open(*file)
		if err != nil {
			glog.Fatal(err)
		}
	}
	glog.V(1).Info("Parsing code generator request")
	req, err := codegenerator.ParseRequest(f)
	if err != nil {
		glog.Fatal(err)
	}
	glog.V(1).Info("Parsed code generator request")
	pkgMap := make(map[string]string)
	if req.Parameter != nil {
		err := parseReqParam(req.GetParameter(), flag.CommandLine, pkgMap)
		if err != nil {
			glog.Fatalf("Error parsing flags: %v", err)
		}
	}

	reg.SetPrefix(*importPrefix)
	reg.SetAllowDeleteBody(*allowDeleteBody)
	reg.SetAllowMerge(*allowMerge)
	reg.SetMergeFileName(*mergeFileName)
	reg.SetUseJSONNamesForFields(*useJSONNamesForFields)
	reg.SetAllowRepeatedFieldsInBody(*allowRepeatedFieldsInBody)
	reg.SetIncludePackageInTags(*includePackageInTags)
	reg.SetUseFQNForOpenAPIName(*useFQNForOpenAPIName)
	reg.SetUseGoTemplate(*useGoTemplate)
	reg.SetEnumsAsInts(*enumsAsInts)
	reg.SetDisableDefaultErrors(*disableDefaultErrors)
	reg.SetSimpleOperationIDs(*simpleOperationIDs)
	reg.SetGenerateUnboundMethods(*generateUnboundMethods)
	if err := reg.SetRepeatedPathParamSeparator(*repeatedPathParamSeparator); err != nil {
		emitError(err)
		return
	}
	reg.SetProject(*project)
	reg.SetoptionsServer(*optionsServer)
	reg.SetoptionsServerPort(*optionsServerPort)
	if *optionsServer != "0.0.0.0" {
		conn, err := net.DialTimeout("tcp", reg.GetoptionsServer(), 3*time.Second)
		if err != nil {
			glog.V(1).Infof("the server %v connection err:%v", reg.GetoptionsServer(), err)
			panic(err)
			return
		}
		conn.Close()
	}

	for k, v := range pkgMap {
		reg.AddPkgMap(k, v)
	}
	g := genopenapi.New(reg)

	if err := genopenapi.AddErrorDefs(reg); err != nil {
		emitError(err)
		return
	}

	if err := reg.Load(req); err != nil {
		emitError(err)
		return
	}

	var targets []*descriptor.File
	for _, target := range req.FileToGenerate {
		f, err := reg.LookupFile(target)
		if err != nil {
			glog.Fatal(err)
		}
		targets = append(targets, f)
	}

	out, err := g.Generate(targets)
	glog.V(1).Info("Processed code generator request")
	if err != nil {
		emitError(err)
		return
	}
	emitFiles(out)
}

func emitFiles(out []*descriptor.ResponseFile) {
	files := make([]*pluginpb.CodeGeneratorResponse_File, len(out))
	for idx, item := range out {
		files[idx] = item.CodeGeneratorResponse_File
	}
	emitResp(&pluginpb.CodeGeneratorResponse{File: files})
}

func emitError(err error) {
	emitResp(&pluginpb.CodeGeneratorResponse{Error: proto.String(err.Error())})
}

func emitResp(resp *pluginpb.CodeGeneratorResponse) {
	buf, err := proto.Marshal(resp)
	if err != nil {
		glog.Fatal(err)
	}
	if _, err := os.Stdout.Write(buf); err != nil {
		glog.Fatal(err)
	}
}

// parseReqParam parses a CodeGeneratorRequest parameter and adds the
// extracted values to the given FlagSet and pkgMap. Returns a non-nil
// error if setting a flag failed.
func parseReqParam(param string, f *flag.FlagSet, pkgMap map[string]string) error {
	if param == "" {
		return nil
	}
	for _, p := range strings.Split(param, ",") {
		spec := strings.SplitN(p, "=", 2)
		if len(spec) == 1 {
			if spec[0] == "allow_delete_body" {
				err := f.Set(spec[0], "true")
				if err != nil {
					return fmt.Errorf("cannot set flag %s: %v", p, err)
				}
				continue
			}
			if spec[0] == "allow_merge" {
				err := f.Set(spec[0], "true")
				if err != nil {
					return fmt.Errorf("cannot set flag %s: %v", p, err)
				}
				continue
			}
			if spec[0] == "allow_repeated_fields_in_body" {
				err := f.Set(spec[0], "true")
				if err != nil {
					return fmt.Errorf("cannot set flag %s: %v", p, err)
				}
				continue
			}
			if spec[0] == "include_package_in_tags" {
				err := f.Set(spec[0], "true")
				if err != nil {
					return fmt.Errorf("cannot set flag %s: %v", p, err)
				}
				continue
			}
			err := f.Set(spec[0], "")
			if err != nil {
				return fmt.Errorf("cannot set flag %s: %v", p, err)
			}
			continue
		}
		name, value := spec[0], spec[1]
		if strings.HasPrefix(name, "M") {
			pkgMap[name[1:]] = value
			continue
		}
		if err := f.Set(name, value); err != nil {
			return fmt.Errorf("cannot set flag %s: %v", p, err)
		}
	}
	return nil
}
