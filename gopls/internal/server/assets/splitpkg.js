// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// data holds the application state, of Go type splitpkg.ResultJSON.
// Each reload() replaces it by the current server state.
var data = null;

// One-time setup.
window.addEventListener('load', function() {
  document.getElementById('add-component').addEventListener('click', onClickAddComponent);
  document.getElementById('assign-apply').addEventListener('click', onClickApplyAssignment);

  reload();
});

/**
 * onClickAddComponent adds a new component, posts it to the server,
 * and reloads the page.
 */
function onClickAddComponent(event) {
  const name = document.getElementById('new-component').value.trim();
  if (name == "") {
    alert("empty component name");
    return;
  }
  if (data.Components.Names.includes(name)) {
    alert("duplicate component name");
    return;
  }
  data.Components.Names.push(name);
  postComponents();
  reload();
}

/**
 * onClickDeleteComponent deletes a component, posts it to the server,
 * and reloads the page.
 */
function onClickDeleteComponent(event) {
  const li = event.target.parentNode;
  li.parentNode.removeChild(li);

  const index = li.index; // of deleted component

  const names = data.Components.Names;
  names.splice(index, 1);

  // Update assignments after implicit renumbering of components.
  const assignments = data.Components.Assignments;
  Object.entries(assignments).forEach(([k, compIndex]) => {
    if (compIndex == index) {
      assignments[k] = 0; // => default component
    } else if (compIndex > index) {
      assignments[k] = compIndex - 1;
    }
  });

  postComponents();
  reload();
}

/** postComponents notifies the server of a change in the Components information. */
function postComponents() {
  // Post the updated components.
  const xhr = new XMLHttpRequest();
  xhr.open("POST", makeURL('/splitpkg-components'), false); // false => synchronous
  xhr.setRequestHeader('Content-Type', 'application/json');
  xhr.send(JSON.stringify(data.Components));
  if (xhr.status === 200) {
    // ok
  } else {
    alert("failed to post new component list: " + xhr.statusText);
    return;
  }
}

/**
 * onClickApplyAssignment is called when the Apply button is clicked.
 * It updates the component assignment mapping.
 */
function onClickApplyAssignment(event) {
  const componentIndex = document.getElementById('assign-select').selectedIndex;

  // Update the component assignment of each spec
  // whose <li class='spec-node'> checkbox is checked.
  document.querySelectorAll('li.spec-node').forEach((specItem) => {
    const checkbox = specItem.firstChild;
    if (checkbox.checked) {
      data.Components.Assignments[specItem.dataset.name] = componentIndex;
    }
  });

  postComponents(); // update the server
  reload(); // recompute the page state
}

/**
 * reload requests the current server state,
 * updates the global 'data' variable,
 * and rebuilds the page DOM to reflect the state.
 */
function reload() {
  const xhr = new XMLHttpRequest();
  xhr.open("GET", makeURL('/splitpkg-json'), false); // false => synchronous
  xhr.send(null);
  if (xhr.status === 200) {
    try {
      data = JSON.parse(xhr.responseText);
    } catch (e) {
      alert("error parsing JSON: " + e);
      return null;
    }
  } else {
    alert("request failed: " + xhr.statusText);
    return null;
  }

  // Ensure there is always a default component.
  if (!data.Components.Names) { // undefined, null, or empty
    data.Components.Names = ["default"];
  }
  if (!data.Components.Assignments) { // undefined, null, or empty
    data.Components.Assignments = {};
  }

  // Rebuild list of components.
  const componentsContainer = document.getElementById('components');
  componentsContainer.replaceChildren(); // clear out previous state
  const assignSelect = document.getElementById('assign-select');
  assignSelect.replaceChildren(); // clear out previous state
  const componentsList = document.createElement('ul');
  componentsContainer.appendChild(componentsList);
  data.Components.Names.forEach((name, i) => {
      // <li>■ name ×
    const li = document.createElement('li');
    li.index = i; // custom index field holds component index (for onClickDeleteComponent)
    componentsList.appendChild(li);

    // ■ name
    const span = document.createElement('span');
    span.className = 'component';
    span.style.color = componentColors[i % componentColors.length];
    span.append('■ ', name)
    li.append(span);

    // ×
    if (i > 0) { // the default component cannot be deleted
      const xSpan = document.createElement('span');
      xSpan.className = 'delete';
      xSpan.append(' ×')
      xSpan.addEventListener('click', onClickDeleteComponent);
      li.append(xSpan);
    }

    // Add component to the assignment dropdown.
    assignSelect.add(new Option(name, null));
  })

  // Rebuild list of decls grouped by file.
  const filesContainer = document.getElementById('files');
  filesContainer.replaceChildren(); // clear out previous state
  data.Files.forEach(fileJson => {
    filesContainer.appendChild(createFileDiv(fileJson));
  });

  // Display strongly connected components.
  const deps = document.getElementById('deps');
  deps.replaceChildren(); // clear out previous state
  const depsList = document.createElement('ul');
  deps.append(depsList);

  // Be explicit if there are no component dependencies.
  if (data.Edges.length == 0) {
    const li = document.createElement('li');
    depsList.append(li);
    li.append("No dependencies");
    return;
  }

  // List all sets of mutually dependent components.
  data.Cycles.forEach((scc) => {
    const item = document.createElement('li');
    item.append("⚠ Component cycle: " +
		scc.map(index => data.Components.Names[index]).join(', '))
    depsList.append(item);
  })

  // Show intercomponent edges.
  data.Edges.forEach((edge) => {
    const edgeItem = document.createElement('li');
    depsList.append(edgeItem);

    // component edge
    const from = data.Components.Names[edge.From];
    const to   = data.Components.Names[edge.To];
    edgeItem.append((edge.Cyclic ? "⚠ " : "") + from + " ➤ " + to);

    // sublist of symbol references that induced the edge
    const refsList = document.createElement('ul');
    edgeItem.appendChild(refsList);
    refsList.className = 'refs-list';
    edge.Refs.forEach(ref => {
      refsList.appendChild(createRefItem(ref));
    });
  })
}

/**
 * makeURL returns a URL string with the specified path,
 * but preserving the current page's query parameters (view, pkg).
 */
function makeURL(path) {
  const url = new URL(window.location.href);
  url.pathname = url.pathname.substring(0, url.pathname.lastIndexOf('/')) + path;
  return url.href;
}

/** createFileDiv creates a <div> for a fileJSON object. */
function createFileDiv(fileData) {
  // Create the main container for the file entry.
  const fileContainer = document.createElement('div');
  fileContainer.className = 'file-node';

  // Create and append the file's base name as a para.
  const para = document.createElement('p');
  fileContainer.appendChild(para);

  // The file's checkbox applies in bulk to all specs within it.
  var specCheckboxes = [];
  const fileCheckbox = document.createElement('input');
  fileCheckbox.type = 'checkbox';
  fileCheckbox.addEventListener('click', (event) => {
    // Select/deselect all specs belonging to the file.
    const checked = event.target.checked;
    specCheckboxes.forEach(checkbox => {
      checkbox.checked = checked;
    });
  })
  para.appendChild(fileCheckbox);
  para.append("File ");

  // Link file name to start of file.
  const baseName = document.createElement('a');
  para.appendChild(baseName);
  baseName.className = 'file-link';
  baseName.textContent = fileData.Base;
  baseName.addEventListener('click', () => httpGET(fileData.URL));

  // Process declarations if they exist.
  if (fileData.Decls && fileData.Decls.length > 0) {
    const declsList = document.createElement('ul');
    declsList.className = 'decls-list';
    // For now we flatten out the decl/spec grouping.
    fileData.Decls.forEach(decl => {
      if (decl.Specs && decl.Specs.length > 0) {
        decl.Specs.forEach(spec => {
          declsList.appendChild(createSpecItem(decl.Kind, spec, specCheckboxes));
        });
      }
    });
    fileContainer.appendChild(declsList);
  }

  return fileContainer;
}

/** createSpecItem creates an <li> element for a specJSON object (one declared name). */
function createSpecItem(kind, specData, checkboxes) {
  // <li><checkbox/>ⓕ <a>myfunc</a>...
  const specItem = document.createElement('li');
  specItem.className = 'spec-node';
  specItem.dataset.name = specData.Name; // custom .name field holds symbol's unique logical name

  // First child is a checkbox.
  const specCheckbox = document.createElement('input');
  specCheckbox.type = 'checkbox';
  specItem.appendChild(specCheckbox);
  checkboxes.push(specCheckbox);

  // Next is the component assignment color swatch.
  const assignSpan = document.createElement('span');
  assignSpan.className = 'component-swatch';
  assignSpan.textContent = "■";
  {
    var index = data.Components.Assignments[specData.Name]; // may be undefined
    if (!index) {
      index = 0; // default
    }
    assignSpan.style.color = componentColors[index % componentColors.length];
    assignSpan.title = "Component " + data.Components.Names[index]; // tooltip
  }
  specItem.appendChild(assignSpan);

  // Encircle the func/var/const/type indicator.
  const symbolSpan = document.createElement('span');
  const symbol = String.fromCodePoint(kind.codePointAt(0) - 'a'.codePointAt(0) + 'ⓐ'.codePointAt(0));
  symbolSpan.title = kind; // tooltip
  symbolSpan.append(`${symbol} `);
  specItem.append(symbolSpan);

  // Link name to declaration.
  const specName = document.createElement('a');
  specItem.appendChild(specName);
  specName.textContent = ` ${specData.Name}`;
  specName.addEventListener('click', () => httpGET(specData.URL));

  return specItem;
}

/** createRefItem creates an <li> element for a refJSON object (a reference). */
function createRefItem(refData) {
  const refItem = document.createElement('li');
  refItem.className = 'ref-node';

  // Link (from -> to) to the reference in from.
  const refLink = document.createElement('a');
  refItem.appendChild(refLink);
  refLink.addEventListener('click', () => httpGET(refData.URL));
  refLink.textContent = "${refData.From} ➤ ${refData.To}";

  return refItem;
}

/** componentColors is a palette of dark, high-contrast colors. */
const componentColors = [
  "#298429",
  "#4B4B8F",
  "#AD2C2C",
  "#A62CA6",
  "#6E65AF",
  "#D15050",
  "#2CA6A6",
  "#C55656",
  "#7B8C58",
  "#587676",
  "#B95EE1",
  "#AF6D41",
];
