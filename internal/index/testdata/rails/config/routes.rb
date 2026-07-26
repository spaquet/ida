Rails.application.routes.draw do
  get "/articles", to: "articles#index"
  resources :comments
end
