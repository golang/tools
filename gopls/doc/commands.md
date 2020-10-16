# Commands

This document describes the LSP-level commands supported by `gopls`. They cannot be invoked directly by users, and all the details are subject to change, so nobody should rely on this information.

<!-- BEGIN Commands: DO NOT MANUALLY EDIT THIS SECTION -->
### **Run go generate**
Identifier: `gopls_generate`

generate runs `go generate` for a given directory.


### **Fill struct**
Identifier: `gopls_fill_struct`

fill_struct is a gopls command to fill a struct with default
values.


### **Regenerate cgo**
Identifier: `gopls_regenerate_cgo`

regenerate_cgo regenerates cgo definitions.


### **Run test(s)**
Identifier: `gopls_test`

test runs `go test` for a specific test function.


### **Run go mod tidy**
Identifier: `gopls_tidy`

tidy runs `go mod tidy` for a module.


### **Undeclared name**
Identifier: `gopls_undeclared_name`

undeclared_name adds a variable declaration for an undeclared
name.


### **Upgrade dependency**
Identifier: `gopls_upgrade_dependency`

upgrade_dependency upgrades a dependency.


### **Run go mod vendor**
Identifier: `gopls_vendor`

vendor runs `go mod vendor` for a module.


### **Extract to variable**
Identifier: `gopls_extract_variable`

extract_variable extracts an expression to a variable.


### **Extract to function**
Identifier: `gopls_extract_function`

extract_function extracts statements to a function.


### **Toggle gc_details**
Identifier: `gopls_gc_details`

gc_details controls calculation of gc annotations.


### **Generate gopls.mod**
Identifier: `gopls_generate_gopls_mod`

generate_gopls_mod (re)generates the gopls.mod file.


<!-- END Commands: DO NOT MANUALLY EDIT THIS SECTION -->
