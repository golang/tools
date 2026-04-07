---
title: "Integrating Interactive Refactoring"
---

This document describes how a language client should interact with `gopls` to support interactive refactorings. This is an experimental feature based on the proposal in [golang/go#76331](https://github.com/golang/go/issues/76331).

<!-- TODO(hxjiang): add link to x/proposal once available -->

## Capabilities

> The prototype implementation in `gopls` differs slightly from the formal LSP proposal. Specifically, it uses the `experimental` section of client or server capabilities rather than introducing new top-level structures.

Client Capabilities

To enable interactive refactoring, the language client must advertise its support for specific input types by adding an `interactiveInputTypes` field to the `experimental` section of its client capabilities.

The value should be a `[]string` containing the types of input UI the client can render.

Example:
```json
{
  // ... existing client capabilities ...
  "experimental": {
    "interactiveInputTypes": [
      "string",
      "enum",
      "bool"
    ]
  }
}
```

Common input types used by `gopls` include:
- `"string"`: A simple text input.
- `"file"`: A file or directory URI picker.
- `"bool"`: A boolean checkbox or toggle.
- `"number"`: A numeric input.
- `"enum"`: A selection from a static set of options.
- `"lazyEnum"`: A selection from a dynamic set of options queried on demand.
- `"list"`: A homogenous list of items.

Server Capabilities

To enable interactive refactoring, the server must advertise its support for resolving commands by adding an `interactiveResolveProvider` field to the `experimental` section of its server capabilities.

```json
{
  // ... existing server capabilities ...
  "experimental": {
    "interactiveResolveProvider": [
        "command"
    ]
  }
}
```

The value should be a `[]string` indicating the supported resolution targets. If `"command"` is present in this list, the client may safely invoke the `command/resolve` method to interactively resolve `ExecuteCommandParams`. Otherwise, the client should not attempt to call this method.

Additional methods may be supported in the future by adding them to this array.

## Request

The `command/resolve` request is sent from the client to the server to interactively resolve `ExecuteCommandParams` for a command before execution. This is useful for operations where the server requires additional input from the user that cannot be determined statically.

When a client receives a `CodeAction` containing a command that supports interactive resolution, it should **not** execute the command immediately via `workspace/executeCommand`. Instead, the client must first send a `command/resolve` request to the server, passing the `ExecuteCommandParams` received in the code action.

The server responds with `ExecuteCommandParams` that may include a `formFields` property. If `formFields` is present and non-empty, it indicates that the command requires user inputs. The client must not proceed with command execution, but must instead present the questions from `formFields` to the user to collect answers. The `formFields` array contains `FormField` objects, each describing a prompt, expected type, and optional default value.

Once the user provides answers, the client sends another `command/resolve` request to the server, populating the `formAnswers` property in the `ExecuteCommandParams` and omitting the `formFields` property. The `formAnswers` array must be of the same length as the `formFields` previously received from the server, with the answer at index `i` corresponding to the question at index `i`.

Upon receiving `formAnswers`, the server validates the input. If the input is invalid, the server returns `ExecuteCommandParams` with `formFields` again, populating the `error` property on the fields that failed validation. The client can then choose to re-render the UI to display these errors and allow the user to correct their input for a retry, or it may abort the operation entirely.

This process repeats until the server returns a response where `formFields` is omitted or empty. This signals that the parameters are fully resolved and valid. At this point, the client may proceed to execute the command by calling `workspace/executeCommand` with the finalized `ExecuteCommandParams` containing the valid `formAnswers`.

Method: `command/resolve`

Params: `ExecuteCommandParams` defined as follows:

```typescript
export interface ExecuteCommandParams extends InteractiveParams {
  // ... original fields (command, arguments) ...
}

export interface InteractiveParams {
	// FormFields defines the questions and validation errors in previous
	// answers to the same questions.
	//
	// This is a server-to-client field. The language server defines these, and
	// the client uses them to render the form.
	//
	// The interactive phase is considered complete when the server returns a
	// response where this slice is omitted.
	formFields?: FormField[];

	// FormAnswers contains the answers for the form questions.
	//
	// When sent by the language server, this field is optional and contains the
	// current or default answers to the questions to support editing previous values.
	//
	// When sent by the language client, this field contains the user's answers.
	// The slice must have the same length as FormFields, where the answer at
	// index i corresponds to the question at index i.
	formAnswers?: any[];
}

// FormField describes a single question in a form and its validation state.
export interface FormField {
	// Description is the text content of the question (the prompt) presented to the user.
	description: string;

	// Type specifies the data type and validation constraints for the answer.
	type: FormFieldType;

	// Default specifies an optional initial value for the answer.
	// If Type is FormFieldTypeEnum, this value must be present in the enum's values array.
	default?: any;

	// Error provides a validation message from the language server.
	// If empty or undefined, the current answer is considered valid.
	error?: string;
}

// FormFieldTypeString defines a text input.
export interface FormFieldTypeString {
	kind: 'string';
}


// FileExistence whether the file denoted by a DocumentURI exists.
//
// It is a bit set allowing combinations of existence states. For
// example, New|Existing allows either state.
export enum FileExistence {
	// New indicates that file has not yet been created.
	New = 1 << 0,
	// Existing indicates that the file exists already.
	Existing = 1 << 1
}

// FileType represents the expected filesystem resource type.
//
// It is a bit set allowing combinations of file types. For example, Regular|Directory
// allows either types.
export enum FileType {
	// Regular indicates the resource could be a regular file.
	Regular = 1 << 0,
	// Directory indicates the resource could be a directory.
	Directory = 1 << 1
}

// FormFieldTypeFile defines an input for a file or directory URI.
//
// The client determines the best mechanism to collect this information from
// the user (e.g., a graphical file picker, a text input with autocomplete, etc).
//
// The value returned by the client must be a valid "DocumentUri" as defined
// in the LSP specification:
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#documentUri
export interface FormFieldTypeFile {
	kind: 'file';

	// Existence constraint.
	existence: FileExistence;

	// Type specifies the set of allowed file types (regular file, directory, etc).
	//
	// Only applicable against existing file.
	type: FileType;
}


// FormFieldTypeBool defines a boolean input.
export interface FormFieldTypeBool {
	kind: 'bool';
}

// FormFieldTypeNumber defines a numeric input.
export interface FormFieldTypeNumber {
	kind: 'number';
}

// FormEnumEntry represents a single option in an enumeration.
export interface FormEnumEntry {
	// Value is the unique string identifier for this option.
	//
	// This is the value that will be sent back to the server in
	// 'FormAnswers' if the user selects this option.
	value: string;

	// Description is the human-readable label presented to the user.
	description: string;
}

// FormFieldTypeEnum defines a selection from a set of values.
//
// Use this type when:
// - The number of options is small (e.g., < 20).
// - All options are known at the time the form is created.
export interface FormFieldTypeEnum {
	kind: 'enum';

	// Name is an optional identifier for the enum type.
	name?: string;

	// Entries is the list of allowable options.
	entries: FormEnumEntry[];
}

// FormFieldTypeLazyEnum defines a selection from a large or dynamic enum entry set.
//
// Use this type when:
//  1. The dataset is too large to send efficiently in a single payload
//     (e.g., thousands of workspace symbols, file uri or cloud resources).
//  2. The available options depend on the user's input (e.g., semantic search).
//  3. Generating the list is expensive and should only be done if requested.
//
// The client is expected to render a search interface (e.g., a combo box with
// a text input) and query the server via 'interactive/listEnum' as the user types.
export interface FormFieldTypeLazyEnum {
	kind: 'lazyEnum';

	// Source identifies the data source on the server.
	//
	// Examples: "workspace/symbol", "database/schema", "git/tags".
	source: string;

	// Config contains the static settings for the source.
	// The client treats this as opaque data and echoes it back in the
	// 'interactive/listEnum' request.
	config?: any;
}

// FormFieldTypeList defines a homogenous list of items.
export interface FormFieldTypeList {
	kind: 'list';

	// ElementType specifies the type of the items in the list.
	// Recursive reference to the union type.
	elementType: FormFieldType;
}
```

Response:
- Result: `ExecuteCommandParams` (the server returns the same params object, potentially modified with new `formFields` or with `formFields` omitted to signal completion).
- error: code and message set in case an exception happens during the code action request.

## Input Types

The following input types are used in `gopls` forms. The client is responsible for collecting valid values for these types:

- **`"string"`**: A simple text value.
- **`"file"`**: A valid Document URI string.
- **`"bool"`**: A boolean value.
- **`"number"`**: A numeric value.
- **`"enum"`**: A selection from a static set of options provided by the server in the `entries` array.
- **`"lazyEnum"`**: A selection from a dynamic set of options queried on demand (see below).
- **`"list"`**: A homogenous list of items.

The `"lazyEnum"` type is used when the set of options is large, dynamic, or expensive to compute (e.g., workspace symbols).

Instead of providing options upfront, the server specifies a `source` and an optional `config` object. As the user provides a search query, the client must call the custom method `interactive/listEnum` to fetch matching options from the server.

Request:
- Method: `interactive/listEnum`
- Params: `InteractiveListEnumParams` defined as follows:

```typescript
export interface InteractiveListEnumParams {
	// Identifies the data source on the server (e.g., "workspace/symbol").
	source: string;

	// Opaque configuration data provided by the server in the form field,
	// which the client must echo back.
	config?: any;

	// The search query entered by the user. An empty string requests a default set.
	query: string;
}
```

Response:
- Result: `FormFieldTypeEnum` containing the matching `entries` for the query.
- error: code and message set in case an exception happens during interactive enum fetching.

## Interaction Example

Here is a concrete example of the interaction flow for `gopls.modify_tags` (adding struct tags):

1. The client requests code actions. The server returns a Code Action containing a `Command`.
   ```json
   {
    "command": {
      "title": "Add struct tags",
      "command": "gopls.modify_tags",
      "arguments": [{ "Modification": "add" }]
    }
   }
   ```

2. Resolution request: Before executing the command, the client calls `command/resolve` passing the `ExecuteCommandParams`.
   ```json
   {
     "command": "gopls.modify_tags",
     "arguments": [{ "Modification": "add" }]
   }
   ```

3. Resolution response: The server responds with `ExecuteCommandParams` containing a `formFields` array.
   ```json
   {
     "command": "gopls.modify_tags",
     "arguments": [{ "Modification": "add" }],
     "formFields": [
       {
         "description": "comma-separated list of tags to add",
         "type": { "kind": "string" },
         "default": "json"
       },
       {
         "description": "transform rule for added tags",
         "type": { "kind": "enum", "entries": [...] },
         "default": "camelcase"
       }
     ]
   }
   ```

4. User fills in form: The client must render UI to collect answers for these fields.

5. Resolution request: The client collects user input and calls `command/resolve` again, populating `formAnswers`.
   ```json
   {
     "command": "gopls.modify_tags",
     "arguments": [{ "Modification": "add" }],
     "formAnswers": ["json,foo", "camelcase"]
   }
   ```

6. Resolution Response: The server validates the input. There are two possible outcomes:

   *   **Case A: Validation Failure**
       If the input is invalid (e.g., the user entered `"json,fo o"` with a space), the server returns `formFields` again with one error per 'invalid' answers. The error is attached to the formFields[i] where the formAnswers[i] is invalid. The client may decide to drop the entire command resolve and command execution or try to return to step 4 to recollect user input.
       ```json
       {
         "command": "gopls.modify_tags",
         "arguments": [{ "Modification": "add" }],
         "formFields": [
           {
             "description": "comma-separated list of tags to add",
             "type": { "kind": "string" },
             "default": "json",
             "error": "cannot contain spaces, quotes, colons, or control characters"
           },
           {
             "description": "transform rule for added tags",
             "type": { "kind": "enum", "entries": [...] },
             "default": "camelcase"
           }
         ],
         "formAnswers": ["json,fo o", "camelcase"]
       }
       ```

   *   **Case B: Success & Execution**
       If the input is valid, the server returns a response with `formFields` omitted.
       ```json
       {
         "command": "gopls.modify_tags",
         "arguments": [{ "Modification": "add" }],
         "formAnswers": ["json,foo", "camelcase"]
       }
       ```
       At this point, the client proceeds to execute the command via `workspace/executeCommand`, passing the finalized params (including `formAnswers`).
       ```json
       {
         "command": "gopls.modify_tags",
         "arguments": [{ "Modification": "add" }],
         "formAnswers": ["json,foo", "camelcase"]
       }
       ```