package tool

import (
	"context"
	"strings"
	"testing"
)

func TestAgentToolSchemaInferredFromTags(t *testing.T) {
	tests := []struct {
		name     AgentTool
		tool     Tool
		required string
		property string
		enum     string
	}{
		{
			name:     AgentToolRead,
			tool:     NewReadTool(),
			required: "path",
			property: "path",
		},
		{
			name:     AgentToolCron,
			tool:     NewCronTool(nil, nil, nil),
			required: "action",
			property: "action",
			enum:     "add",
		},
		{
			name:     AgentToolUpdateTodos,
			tool:     NewUpdateTodosTool(nil),
			required: "todos",
			property: "todos",
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			info, err := tt.tool.Info(context.Background())
			if err != nil {
				t.Fatalf("info: %v", err)
			}
			if info.Name != string(tt.name) {
				t.Fatalf("name = %q, want %q", info.Name, tt.name)
			}

			js, err := info.ParamsOneOf.ToJSONSchema()
			if err != nil {
				t.Fatalf("json schema: %v", err)
			}
			if !hasRequiredField(js.Required, tt.required) {
				t.Fatalf("required = %v, want %q", js.Required, tt.required)
			}
			prop, ok := js.Properties.Get(tt.property)
			if !ok {
				t.Fatalf("property %q not found", tt.property)
			}
			if prop.Description == "" {
				t.Fatalf("property %q description is empty", tt.property)
			}
			if tt.enum != "" && !hasEnumValue(prop.Enum, tt.enum) {
				t.Fatalf("enum = %v, want %q", prop.Enum, tt.enum)
			}
		})
	}
}

func TestAgentToolKeepsOriginalErrorShape(t *testing.T) {
	todosTool := NewUpdateTodosTool(nil)

	_, err := todosTool.InvokableRun(context.Background(), `{"todos":[{"content":"Unknown state","status":"later"}]}`)
	if err == nil {
		t.Fatalf("expected invalid status error")
	}
	if strings.Contains(err.Error(), "[LocalFunc]") {
		t.Fatalf("error leaked Eino wrapper: %v", err)
	}
}

func hasRequiredField(required []string, want string) bool {
	for _, field := range required {
		if field == want {
			return true
		}
	}
	return false
}

func hasEnumValue(enum []any, want string) bool {
	for _, value := range enum {
		if value == want {
			return true
		}
	}
	return false
}
