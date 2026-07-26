module Publishable
end

class Article
  has_many :comments
  broadcasts_to :article

  def publish
  end
end
