"use strict";

const form = document.querySelector("#graphql-form");
const queryInput = document.querySelector("#graphql-query");
const resultOutput = document.querySelector("#graphql-result");

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  resultOutput.textContent = "Loading…";
  try {
    const response = await fetch("/api/graphql", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ query: queryInput.value })
    });
    const payload = await response.json();
    resultOutput.textContent = JSON.stringify(payload, null, 2);
  } catch {
    resultOutput.textContent = "Request failed. Check the server connection and try again.";
  }
});
