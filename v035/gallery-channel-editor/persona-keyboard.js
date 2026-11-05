async(page)=>{
 await page.reload();await page.setViewportSize({width:1440,height:1100});await page.emulateMedia({reducedMotion:"reduce"});
 const hit=async(name,option=false)=>{if(option){const opts=await page.getByRole("option").allTextContents();const index=opts.findIndex(x=>x.trim()===name);if(index<0)throw Error(name);for(let n=0;n<opts.length;n++)await page.keyboard.press("ArrowUp");for(let n=0;n<index;n++)await page.keyboard.press("ArrowDown");await page.keyboard.press("Enter");return;}const el=page.getByRole("button",{name,exact:true}).last();await el.focus();await el.press("Enter");};
 await page.getByRole("button",{name:/3 titles · 20:00–23:00/}).focus();await page.keyboard.press("Enter");
 await page.getByRole("textbox",{name:"Block name",exact:true}).fill("Prime-time movies");await hit("Apply");
 await hit("Add");await hit("Daypart");await hit("Daypart preset");await hit("Morning",true);await hit("Source type");await hit("Collection",true);await hit("Collection");await hit("Family movie night",true);await hit("Apply");
 await hit("Add pinned slot");await page.getByRole("textbox",{name:"Block name",exact:true}).fill("Weeknight sitcom strip");await page.getByRole("textbox",{name:"Find a TV show",exact:true}).fill("The Crimson Harbor");await page.getByRole("button",{name:/The Crimson Harbor/}).focus();await page.keyboard.press("Enter");await hit("Apply");
 await hit("Break placement");await hit("Natural break points",true);await hit("Break pattern");await hit("1980s",true);await hit("Commercials from Assets");await hit("Saturday morning ads",true);
 for(const name of ["Ident when tuning in","Up next","Station IDs"]){const el=page.getByRole("switch",{name,exact:true});await el.focus();if(!(await el.isChecked()))await page.keyboard.press("Space");}
 await hit("Save channel");
 await page.getByRole("tab",{name:"Style",exact:true}).focus();await page.keyboard.press("Enter");await hit("Style");await page.getByRole("menuitemradio",{name:/NEON 84/}).focus();await page.keyboard.press("Enter");await hit("Save channel");await page.getByRole("tab",{name:"Programming",exact:true}).focus();await page.keyboard.press("Enter");
 await page.getByRole("heading",{name:"Dayparts and programme blocks",exact:true}).evaluate(el=>el.scrollIntoView({block:"center"}));await page.screenshot({path:"v035/gallery-channel-editor/persona-accessibility.png"});
}
