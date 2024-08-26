package gen

import (
	"encoding/json"
	"fmt"
	"google.golang.org/protobuf/types/descriptorpb"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"

	"github.com/go-kratos/kratos/tool/protobuf/pkg/gen"
	"github.com/go-kratos/kratos/tool/protobuf/pkg/generator"
	"github.com/go-kratos/kratos/tool/protobuf/pkg/naming"
	"github.com/go-kratos/kratos/tool/protobuf/pkg/tag"
	"github.com/go-kratos/kratos/tool/protobuf/pkg/typemap"
)

type swaggerGen struct {
	generator.Base
	// defsMap will fill into swagger's definitions
	// key is full qualified proto name
	defsMap map[string]*typemap.MessageDefinition
}

// NewSwaggerGenerator a swagger generator
func NewSwaggerGenerator() *swaggerGen {
	return &swaggerGen{}
}

func (t *swaggerGen) Generate(in *plugin.CodeGeneratorRequest) *plugin.CodeGeneratorResponse {
	t.Setup(in)
	resp := &plugin.CodeGeneratorResponse{}
	for _, f := range t.GenFiles {
		if len(f.Service) == 0 {
			continue
		}
		respFile := t.generateSwagger(f)
		if respFile != nil {
			resp.File = append(resp.File, respFile)
		}
	}
	return resp
}

func (t *swaggerGen) generateSwagger(file *descriptor.FileDescriptorProto) *plugin.CodeGeneratorResponse_File {
	var pkg = file.GetPackage()
	r := regexp.MustCompile("v(\\d+)$")
	strs := r.FindStringSubmatch(pkg)
	var vStr string
	if len(strs) >= 2 {
		vStr = strs[1]
	} else {
		vStr = ""
	}
	var swaggerObj = &swaggerObject{
		Paths:   swaggerPathsObject{},
		Swagger: "2.0",
		Info: swaggerInfoObject{
			Title:   file.GetName(),
			Version: vStr,
		},
		Schemes:  []string{"http", "https"},
		Consumes: []string{"application/json", "multipart/form-data"},
		Produces: []string{"application/json"},
	}
	t.defsMap = map[string]*typemap.MessageDefinition{}

	out := &plugin.CodeGeneratorResponse_File{}
	name := naming.GenFileName(file, ".swagger.json")
	for idx, svc := range file.Service {
		for _, meth := range svc.Method {
			if !t.ShouldGenForMethod(file, svc, meth) {
				continue
			}
			apiInfo := t.GetHttpInfoCached(file, svc, meth)
			pathItem := swaggerPathItemObject{}
			if originPathItem, ok := swaggerObj.Paths[apiInfo.Path]; ok {
				pathItem = originPathItem
			}
			op := t.getOperationByHTTPMethod(apiInfo.HttpMethod, &pathItem)
			op.Summary = apiInfo.Title
			op.Description = apiInfo.Description
			swaggerObj.Paths[apiInfo.Path] = pathItem
			tags := getServiceComment(idx, file.SourceCodeInfo.Location)
			if tags == "" {
				tags = pkg + "." + svc.GetName()
			}
			op.Tags = []string{tags}

			// request
			request := t.Reg.MessageDefinition(meth.GetInputType())
			// request cannot represent by simple form
			isComplexRequest := false
			for _, field := range request.Descriptor.Field {
				if !generator.IsScalar(field) {
					isComplexRequest = true
					break
				}
			}
			//log.Println("isComplexRequest = ", isComplexRequest)
			if !isComplexRequest && apiInfo.HttpMethod == "GET" {
				for _, field := range request.Descriptor.Field {
					if !generator.IsScalar(field) {
						continue
					}
					p := t.getQueryParameter(file, request, field)
					op.Parameters = append(op.Parameters, p)
				}
			} else {
				p := swaggerParameterObject{}
				p.In = "body"
				p.Required = true
				p.Name = "body"
				p.Schema = &swaggerSchemaObject{}
				p.Schema.Ref = "#/definitions/" + meth.GetInputType()
				op.Parameters = []swaggerParameterObject{p}
			}

			// response
			resp := swaggerResponseObject{}
			resp.Description = "A successful response."

			// proto 里面的response只定义data里面的
			// 所以需要把code msg data 这一级加上
			resp.Schema.Type = "object"
			resp.Schema.Properties = &swaggerSchemaObjectProperties{}
			p := keyVal{Key: "code", Value: &schemaCore{Type: "integer"}}
			*resp.Schema.Properties = append(*resp.Schema.Properties, p)
			p = keyVal{Key: "msg", Value: &schemaCore{Type: "string"}}
			*resp.Schema.Properties = append(*resp.Schema.Properties, p)
			p = keyVal{Key: "data", Value: schemaCore{Ref: "#/definitions/" + meth.GetOutputType()}}
			*resp.Schema.Properties = append(*resp.Schema.Properties, p)
			op.Responses = swaggerResponsesObject{"200": resp}
		}
	}

	// walk though definitions
	t.walkThroughFileDefinition(file)
	defs := swaggerDefinitionsObject{}
	swaggerObj.Definitions = defs
	for typ, msg := range t.defsMap {
		def := swaggerSchemaObject{}
		def.Properties = new(swaggerSchemaObjectProperties)
		def.Description = strings.Trim(msg.Comments.Leading, "\n\r ")
		for _, field := range msg.Descriptor.Field {
			p := keyVal{Key: generator.GetFormOrJSONName(field)}
			schema := t.schemaForField(file, msg, field)
			if GetFieldRequired(field, t.Reg, msg) {
				def.Required = append(def.Required, p.Key)
			}
			p.Value = schema
			*def.Properties = append(*def.Properties, p)
		}
		def.Type = "object"
		defs[typ] = def
	}
	b, _ := json.MarshalIndent(swaggerObj, "", "    ")
	str := string(b)
	out.Name = &name
	out.Content = &str
	return out
}

func (t *swaggerGen) getOperationByHTTPMethod(httpMethod string, pathItem *swaggerPathItemObject) *swaggerOperationObject {
	var op = &swaggerOperationObject{}
	switch httpMethod {
	case http.MethodGet:
		pathItem.Get = op
	case http.MethodPost:
		pathItem.Post = op
	case http.MethodPut:
		pathItem.Put = op
	case http.MethodDelete:
		pathItem.Delete = op
	case http.MethodPatch:
		pathItem.Patch = op
	default:
		pathItem.Get = op
	}
	return op
}

func (t *swaggerGen) getQueryParameter(file *descriptor.FileDescriptorProto,
	input *typemap.MessageDefinition,
	field *descriptor.FieldDescriptorProto) swaggerParameterObject {
	p := swaggerParameterObject{}
	p.Name = generator.GetFormOrJSONName(field)
	fComment, _ := t.Reg.FieldComments(input, field)
	cleanComment := tag.GetCommentWithoutTag(fComment.Leading)

	p.Description = strings.Trim(strings.Join(cleanComment, "\n"), "\n\r ")
	validateComment := getValidateComment(field)
	if p.Description != "" && validateComment != "" {
		p.Description = p.Description + "," + validateComment
	} else if validateComment != "" {
		p.Description = validateComment
	}

	if field.GetType().String() == "TYPE_ENUM" {
		comment := t.getEnumComment(field.GetTypeName())
		if comment != "" {
			p.Description = p.Description + comment
		}
	}
	p.In = "query"
	p.Required = GetFieldRequired(field, t.Reg, input)
	typ, isArray, format := getFieldSwaggerType(field)
	if isArray {
		p.Items = &swaggerItemsObject{}
		p.Type = "array"
		p.Items.Type = typ
		p.Items.Format = format
	} else {
		p.Type = typ
		p.Format = format
	}
	return p
}

func GetFieldRequired(
	f *descriptor.FieldDescriptorProto,
	reg *typemap.Registry,
	md *typemap.MessageDefinition,
) bool {
	fComment, _ := reg.FieldComments(md, f)
	var tags []reflect.StructTag
	{
		//get required info from gogoproto.moretags
		moretags := tag.GetMoreTags(f)
		if moretags != nil {
			tags = []reflect.StructTag{reflect.StructTag(*moretags)}
		}
	}
	if len(tags) == 0 {
		tags = tag.GetTagsInComment(fComment.Leading)
	}
	validateTag := tag.GetTagValue("validate", tags)
	var validateRules []string
	if validateTag != "" {
		validateRules = strings.Split(validateTag, ",")
	}
	//log.Println("validateRules = ", validateRules)
	required := false
	for _, rule := range validateRules {
		if strings.Contains(rule, "required") || strings.Contains(rule, "require") {
			required = true
		}
		//if rule == "required" || rule == "require" {
		//	required = true
		//}
	}
	return required
}

func (t *swaggerGen) schemaForField(file *descriptor.FileDescriptorProto,
	msg *typemap.MessageDefinition,
	field *descriptor.FieldDescriptorProto) swaggerSchemaObject {
	schema := swaggerSchemaObject{}
	fComment, err := t.Reg.FieldComments(msg, field)
	if err != nil {
		gen.Error(err, "comment not found err %+v")
	}
	schema.Description = strings.Trim(fComment.Leading, "\n\r ")
	validateComment := getValidateComment(field)
	if schema.Description != "" {
		//schema.Description = schema.Description + "," + validateComment
	} else if validateComment != "" {
		schema.Description = validateComment
	}
	if field.GetType().String() == "TYPE_ENUM" {
		comment := t.getEnumComment(field.GetTypeName())
		if comment != "" {
			schema.Description = schema.Description + comment
		}
	}
	typ, isArray, format := getFieldSwaggerType(field)
	if !generator.IsScalar(field) {
		if generator.IsMap(field, t.Reg) {
			schema.Type = "object"
			mapMsg := t.Reg.MessageDefinition(field.GetTypeName())
			mapValueField := mapMsg.Descriptor.Field[1]
			valSchema := t.schemaForField(file, mapMsg, mapValueField)
			schema.AdditionalProperties = &valSchema
		} else {
			if isArray {
				schema.Items = &swaggerItemsObject{}
				schema.Type = "array"
				schema.Items.Ref = "#/definitions/" + field.GetTypeName()
			} else {
				schema.Ref = "#/definitions/" + field.GetTypeName()
			}
		}
	} else {
		if isArray {
			schema.Items = &swaggerItemsObject{}
			schema.Type = "array"
			schema.Items.Type = typ
			schema.Items.Format = format
		} else {
			schema.Type = typ
			schema.Format = format
		}
	}
	return schema
}

func (t *swaggerGen) walkThroughFileDefinition(file *descriptor.FileDescriptorProto) {
	for _, svc := range file.Service {
		for _, meth := range svc.Method {
			shouldGen := t.ShouldGenForMethod(file, svc, meth)
			if !shouldGen {
				continue
			}
			t.walkThroughMessages(t.Reg.MessageDefinition(meth.GetOutputType()))
			t.walkThroughMessages(t.Reg.MessageDefinition(meth.GetInputType()))
		}
	}
}

func (t *swaggerGen) walkThroughMessages(msg *typemap.MessageDefinition) {
	_, ok := t.defsMap[msg.ProtoName()]
	if ok {
		return
	}
	if !msg.Descriptor.GetOptions().GetMapEntry() {
		t.defsMap[msg.ProtoName()] = msg
	}
	for _, field := range msg.Descriptor.Field {
		if field.GetType() == descriptor.FieldDescriptorProto_TYPE_MESSAGE {
			t.walkThroughMessages(t.Reg.MessageDefinition(field.GetTypeName()))
		}
	}
}

func (t *swaggerGen) getEnumComment(enumName string) string {
	var comments []string
	for _, item := range t.Base.GenFiles {
		if len(item.EnumType) > 0 {
			for i, eItem := range item.EnumType {
				name := "." + item.GetPackage() + "." + *eItem.Name
				if name == enumName {
					for i2, vItem := range eItem.Value {
						comment := getEnumFieldComment(int32(i), int32(i2), item.SourceCodeInfo.Location)
						if comment != "" {
							comment = strings.Replace(comment, "\n", "", -1)
							comments = append(comments, fmt.Sprintf("%d", *vItem.Number)+":"+comment)
						}
					}
					break
				}
			}
		}
	}
	if len(comments) > 0 {
		return "【" + strings.Join(comments, " ") + "】"
	}
	return ""
}

func getEnumFieldComment(enumMsgIdx int32, enumFieldIdx int32, location []*descriptorpb.SourceCodeInfo_Location) string {
	path := []int32{5, enumMsgIdx, 2, enumFieldIdx}
	comment := ""
	for _, infoLocation := range location {
		if pathEqual(path, infoLocation.Path) {
			if len(infoLocation.GetLeadingDetachedComments()) > 0 {
				return strings.Join(infoLocation.GetLeadingDetachedComments(), " ")
			} else {
				return infoLocation.GetLeadingComments()
			}
		}
	}
	return comment
}

func getFieldSwaggerType(field *descriptor.FieldDescriptorProto) (typeName string, isArray bool, formatName string) {
	typeName = "unknown"
	switch field.GetType() {
	case descriptor.FieldDescriptorProto_TYPE_BOOL:
		typeName = "boolean"
	case descriptor.FieldDescriptorProto_TYPE_DOUBLE:
		typeName = "double"
		formatName = "double"
	case descriptor.FieldDescriptorProto_TYPE_FLOAT:
		typeName = "float"
		formatName = "float"
	case
		descriptor.FieldDescriptorProto_TYPE_INT64,
		descriptor.FieldDescriptorProto_TYPE_UINT64,
		descriptor.FieldDescriptorProto_TYPE_INT32,
		descriptor.FieldDescriptorProto_TYPE_FIXED64,
		descriptor.FieldDescriptorProto_TYPE_FIXED32,
		descriptor.FieldDescriptorProto_TYPE_ENUM,
		descriptor.FieldDescriptorProto_TYPE_UINT32,
		descriptor.FieldDescriptorProto_TYPE_SFIXED32,
		descriptor.FieldDescriptorProto_TYPE_SFIXED64,
		descriptor.FieldDescriptorProto_TYPE_SINT32,
		descriptor.FieldDescriptorProto_TYPE_SINT64:
		typeName = "integer"
	case
		descriptor.FieldDescriptorProto_TYPE_STRING,
		descriptor.FieldDescriptorProto_TYPE_BYTES:
		typeName = "string"
	case descriptor.FieldDescriptorProto_TYPE_MESSAGE:
		typeName = "object"
	}
	if field.Label != nil && *field.Label == descriptor.FieldDescriptorProto_LABEL_REPEATED {
		isArray = true
	}
	return
}

func getValidateComment(field *descriptor.FieldDescriptorProto) string {
	var (
		tags []reflect.StructTag
	)
	//get required info from gogoproto.moretags
	moretags := tag.GetMoreTags(field)
	if moretags != nil {
		tags = []reflect.StructTag{reflect.StructTag(*moretags)}
	}
	validateTag := tag.GetTagValue("validate", tags)

	// trim
	regStr := []string{
		"required *,*",
		"omitempty *,*",
	}
	for _, v := range regStr {
		re, _ := regexp.Compile(v)
		validateTag = re.ReplaceAllString(validateTag, "")
	}
	return validateTag
}

func getServiceComment(index int, location []*descriptorpb.SourceCodeInfo_Location) string {
	path := []int32{6, int32(index)}
	comment := ""
	for _, infoLocation := range location {
		if pathEqual(path, infoLocation.Path) {
			//log.Println("GetLeadingDetachedComments = ", infoLocation.GetLeadingDetachedComments())

			if len(infoLocation.GetLeadingDetachedComments()) > 0 {
				return strings.Join(infoLocation.GetLeadingDetachedComments(), " ")
			} else {
				return infoLocation.GetLeadingComments()
			}
		}
	}
	return comment
}

func pathEqual(path1, path2 []int32) bool {
	if len(path1) != len(path2) {
		return false
	}
	for i, v := range path1 {
		if path2[i] != v {
			return false
		}
	}
	return true
}
