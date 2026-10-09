package core

import (
	"context"
	"encoding/json"
)

func (s *Store) Seed(ctx context.Context) error {
	type seed struct {
		title, slug, excerpt, category string
		tags                           []string
		body                           string
	}
	examples := []seed{
		{"A little less noise. A little more meaning.", "a-little-less-noise", "On making room for the work, the people, and the small things that deserve our attention.", "Notes on living", []string{"Attention", "Slow living"}, "There is a particular kind of quiet that arrives when you stop trying to fill every space.\n\nNot the absence of life, but the presence of it. The cup cooling on the desk. Light moving across a wall. An idea that finally has enough room to become itself.\n\n## An experiment in paying attention\n\nFor a week, I began each morning with a notebook instead of a screen. No grand routine. Just ten minutes, a few lines, and permission to notice what was already there.\n\n> Attention is how we tell the world what matters.\n\nThe first page was a list of things I thought I should do. By Thursday, it was a list of things I did not want to miss.\n\n## Make a smaller space\n\n- Leave one hour unclaimed\n- Take the long way home\n- Write one sentence that is entirely your own\n\nA good day does not always need more in it. Sometimes it needs a little less.\n\n---\n\n*Fictional demonstration essay. Replace it with your own story.*"},
		{"Building things that feel human", "building-things-that-feel-human", "A few principles for software that respects your time and leaves room for curiosity.", "Making things", []string{"Design", "Technology"}, "The best tools disappear just enough to let us do our work.\n\n## Start with care\n\nUseful software is a conversation, not a collection of features. Each button makes a promise. Every error message is a chance to keep that promise.\n\n```go\n// Small tools. Clear intentions.\nfunc Begin() string {\n    return \"make something meaningful\"\n}\n```\n\n| Principle | In practice |\n| --- | --- |\n| Clarity | Say what happens next |\n| Agency | Make changes reversible |\n| Care | Preserve the writer's work |\n\n*Fictional demonstration essay.*"},
		{"The art of taking the long way home", "the-long-way-home", "A familiar city, a different turn, and the unexpected joy of getting a little lost.", "Out in the world", []string{"Wandering", "Places"}, "I turned left where I usually turn right. That was the whole plan.\n\nThe city answered with a tiny bookshop, an open window, and a park I had walked past for years without seeing.\n\n## A map of small discoveries\n\nWe travel far to experience novelty. Sometimes all it takes is walking home without the efficiency of a destination.\n\n*Fictional demonstration essay.*"},
		{"A notebook for unfinished thoughts", "a-notebook-for-unfinished-thoughts", "Not every idea needs an ending. Some just need somewhere to land.", "Creative practice", []string{"Writing", "Process"}, "This is a permission slip for the unfinished.\n\nFor the opening paragraph with nowhere to go. For the sketch in the margin. For the question you cannot stop asking.\n\n## A small practice\n\n1. Notice something\n2. Write it down\n3. Let it stay unresolved\n\nSeeds do not look like gardens. Give them time.\n\n*Fictional demonstration essay.*"},
		{"慢下来，才听得见", "slow-down-and-listen", "给日常留一点空白，让那些微小而真实的感受浮现出来。", "Notes on living", []string{"随笔", "Attention"}, "清晨的光落在桌面上，咖啡还冒着热气。今天，不急着打开所有窗口。\n\n## 给思考一点时间\n\n记录并不是为了立即得出结论。它让我们看见，原来普通的一天里，也藏着值得停留的片刻。\n\n> 留白不是空缺，而是邀请。\n\n- 走一条没有走过的小路\n- 写下一件让你微笑的事\n- 给一个未完成的想法留个位子\n\n*虚构示例文章，可替换为你自己的内容。*"},
		{"A place for ideas to grow", "a-place-for-ideas-to-grow", "A personal corner of the internet, built for ownership, curiosity, and the long view.", "Making things", []string{"Digital garden", "Open web"}, "A personal website is a small act of optimism. It says: these thoughts are worth tending.\n\n## Own the quiet corner\n\nWrite in Markdown. Keep your originals. Publish deliberately. Change your mind without losing where you began.\n\nAn agent can help arrange the desk. The story remains yours.\n\n*Fictional demonstration essay.*"},
	}
	for i, v := range examples {
		a := map[string]any{"title": v.title, "slug": v.slug, "excerpt": v.excerpt, "category": v.category, "tags": v.tags, "markdown": v.body, "featured": i == 0}
		b, _ := json.Marshal(a)
		res, e := s.Call(ctx, "posts.create", b, "demo-seed")
		if e != nil {
			return e
		}
		p := res.(map[string]any)["post"].(Post)
		b, _ = json.Marshal(map[string]any{"id": p.ID, "expected_revision": p.Revision, "confirm": true})
		if _, e = s.Call(ctx, "posts.publish", b, "demo-seed"); e != nil {
			return e
		}
	}
	return nil
}
