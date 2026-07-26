import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["output"]
  static values = { url: String }

  connect() {
  }

  greet() {
    this.outputTarget.textContent = "Hello"
  }
}
